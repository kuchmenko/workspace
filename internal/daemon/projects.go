package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

func (r *Reconciler) reconcileProjects(ws *config.Workspace) {
	machine := loadMachineName()
	now := time.Now()
	dirty := false
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		if !proj.SyncEnabled() {
			r.logger.Printf("reconciler: %s auto_sync=false, fetch only", name)
		}
		if bs, ok := r.backoff[name]; ok && now.Before(bs.nextAllowedAt) {
			continue
		}
		touched := false
		if err := r.syncProject(name, &proj, machine, &touched); err != nil {
			r.recordBackoff(name, err)
		} else {
			r.resetBackoff(name)
		}
		if touched {
			ws.Projects[name] = proj
			dirty = true
		}
	}
	if dirty {
		// Persist metadata refreshes (last_active_*, KindBranchOrphan
		// clearings) so Phase 1 of the next tick commits and pushes
		// them. Save's empty-machines GC also fires here, completing
		// the legacy-autopush migration started at Load time.
		if err := config.Save(r.root, ws); err != nil {
			r.logger.Printf("reconciler: save workspace.toml after metadata refresh: %v", err)
		}
	}
}

func (r *Reconciler) syncProject(name string, proj *config.Project, machine string, touched *bool) error {
	mainPath := filepath.Join(r.root, proj.Path)
	barePath := layout.BarePath(mainPath)

	// Layout check: classify on-disk state and route accordingly.
	bareMissing := false
	mainMissing := false
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		bareMissing = true
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		mainMissing = true
	}

	if bareMissing && mainMissing {
		// Project is registered in workspace.toml but nothing exists on
		// disk. Auto-clone if enabled. Sequential semantics happen for
		// free: this clone is the only filesystem op for this project on
		// this tick, the next project's loop iteration runs after, and
		// the next tick reuses the now-present bare branch.
		if !r.autoBootstrap || !proj.SyncEnabled() {
			return nil
		}
		return r.autoCloneMissing(name, *proj)
	}

	if bareMissing {
		// mainPath exists, no bare → plain checkout drift, needs migrate.
		r.recordProjectConflict(name, "", conflict.KindNeedsMigration, fmt.Sprintf("plain checkout at %s", mainPath))
		return nil
	}

	// One-time repair for bare repos created before the SetFetchRefspec
	// fix: if no remote.origin.fetch is configured, the upcoming Fetch
	// would update only FETCH_HEAD and leave refs/remotes/origin/* empty,
	// breaking AheadBehind and ff-pull for the main worktree. Best-effort:
	// a failure here is logged via the fetch error path below, since we
	// still attempt the fetch unconditionally.
	if !git.HasFetchRefspec(barePath) {
		if err := git.SetFetchRefspec(barePath); err != nil {
			r.logger.Printf("reconciler: %s: repair fetch refspec: %v", name, err)
		}
	}

	if err := git.Fetch(barePath); err != nil {
		return err // counts toward backoff
	}

	// auto_sync=false → fetch only, no push or pull.
	if !proj.SyncEnabled() {
		return nil
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return err
	}

	for _, wt := range wts {
		if wt.Bare || wt.Detached || wt.Branch == "" {
			continue
		}

		// "Main worktree" is strictly the one at proj.Path. We do NOT treat
		// any worktree on default_branch as main, because git allows --force
		// attaching another worktree to that branch and we don't want to
		// accidentally ff-pull a non-main checkout.
		isMain := wt.Path == mainPath

		// Skip anything where the user is mid-edit.
		if git.HasIndexLock(wt.Path) {
			continue
		}

		// Main worktree on the project's default branch → ff-pull when safe.
		if isMain {
			if git.IsDirty(wt.Path) {
				continue
			}
			ahead, behind, has := git.AheadBehind(wt.Path, wt.Branch)
			if !has {
				continue
			}
			if behind > 0 && ahead == 0 {
				if err := git.Pull(wt.Path); err != nil {
					r.recordProjectConflict(name, wt.Branch, conflict.KindMainDivergence, err.Error())
					continue
				}
				_ = r.clearProjectConflict(name, wt.Branch, conflict.KindMainDivergence)
			} else if ahead > 0 && behind > 0 {
				r.recordProjectConflict(name, wt.Branch, conflict.KindMainDivergence,
					fmt.Sprintf("ahead %d, behind %d — main worktree should not be diverged", ahead, behind))
			}
			continue
		}

		// Sibling worktrees: no push from the daemon. Refresh metadata for
		// branches the user is actively committing to so `ws worktree list`
		// and the workspace.toml registry reflect the latest activity.
		// Branches not yet in [[branches]] (legacy wt/<machine>/* checkouts
		// that pre-date this PR) are silently skipped — they'll get
		// re-registered when the user runs `ws worktree add` against them.
		if machine != "" && proj.LookupBranch(wt.Branch) != nil {
			ahead, _, has := git.AheadBehind(wt.Path, wt.Branch)
			if has && ahead > 0 {
				if proj.TouchActive(wt.Branch, machine, time.Now()) {
					*touched = true
				}
			}
		}
	}

	// Branch-orphan detection: any registered branch whose last_pushed_at
	// is set was observed on origin at least once, so its origin ref
	// should still exist post-fetch. If it doesn't — the branch was
	// deleted on origin (typical: PR merged with auto-delete-branch).
	// Record the orphan and let the user decide via `ws sync resolve`.
	// Re-appearance on the next tick auto-clears the conflict.
	//
	// Branches with empty last_pushed_at are local-only (created via
	// `ws worktree add` and never pushed) — origin's missing ref is
	// expected and must NOT trip orphan detection.
	for _, b := range proj.Branches {
		if b.LastPushedAt == "" {
			_ = r.clearProjectConflict(name, b.Name, conflict.KindBranchOrphan)
			continue
		}
		if git.HasRemoteBranch(barePath, "origin", b.Name) {
			_ = r.clearProjectConflict(name, b.Name, conflict.KindBranchOrphan)
			continue
		}
		details := fmt.Sprintf("origin ref refs/remotes/origin/%s missing post-fetch (last pushed by %s at %s)",
			b.Name, b.LastPushedMachine, b.LastPushedAt)
		r.recordProjectConflict(name, b.Name, conflict.KindBranchOrphan, details)
	}

	return nil
}

// autoCloneMissing handles the "registered in workspace.toml but nothing on
// disk" case. Called from syncProject when both <path>.bare and <path> are
// absent and AutoBootstrap is enabled. Sequential by construction: one clone
// happens per project per tick, after which the project takes the existing-
// bare branch on subsequent ticks.
//
// Error mapping:
//   - ErrNeedsBootstrap → conflict 'needs-bootstrap' (default branch ambiguous)
//   - ErrPathBlocked    → conflict 'path-blocked'    (shouldn't really happen here, but defensive)
//   - any other error   → returned to caller, which feeds it into per-project
//     exponential backoff (network/auth flakes are the common case)
//
// On success, proj.DefaultBranch may have been filled in by CloneIntoLayout;
// we persist workspace.toml in place so the next tick (and the rest of the
// fleet via the workspace.toml sync) sees the new value.
func (r *Reconciler) autoCloneMissing(name string, proj config.Project) error {
	r.logger.Printf("reconciler: auto-clone %s from %s", name, proj.Remote)

	res, err := clone.CloneIntoLayout(r.root, name, &proj, clone.Options{
		Logf: r.logger.Printf,
		// Non-interactive: PromptDefaultBranch nil → ErrNeedsBootstrap if
		// the branch can't be auto-detected.
	})
	if err != nil {
		switch {
		case errors.Is(err, clone.ErrNeedsBootstrap):
			r.recordProjectConflict(name, "", conflict.KindNeedsBootstrap,
				"default branch could not be auto-detected — run `ws bootstrap "+name+"`")
			return nil
		case errors.Is(err, clone.ErrPathBlocked):
			r.recordProjectConflict(name, "", conflict.KindPathBlocked,
				"non-repo files at project path — clean up manually and re-run")
			return nil
		case errors.Is(err, clone.ErrNeedsMigration), errors.Is(err, clone.ErrAlreadyCloned):
			// Both indicate disk state changed under us between the stat
			// and the clone. Treat as a no-op; the next tick will route
			// the project through the normal sync path.
			return nil
		default:
			r.recordProjectConflict(name, "", conflict.KindCloneFailed, err.Error())
			return err
		}
	}

	r.logger.Printf("reconciler: cloned %s → %s (default_branch=%s)", name, res.BarePath, res.DefaultBranch)
	// Clear any previously recorded clone failure for this project.
	_ = r.clearProjectConflict(name, "", conflict.KindCloneFailed)
	_ = r.clearProjectConflict(name, "", conflict.KindNeedsBootstrap)

	// Persist default_branch back into workspace.toml. We re-load from disk
	// to avoid trampling unrelated edits the user (or another reconciler
	// for a different workspace) may have made between Phase 1 and now.
	if proj.DefaultBranch != "" {
		fresh, err := config.Load(r.root)
		if err != nil {
			r.logger.Printf("reconciler: reload workspace.toml after clone: %v", err)
			return nil
		}
		stored, ok := fresh.Projects[name]
		if !ok {
			return nil // project was removed from registry mid-tick; nothing to update
		}
		if stored.DefaultBranch == "" {
			stored.DefaultBranch = proj.DefaultBranch
			fresh.Projects[name] = stored
			if err := config.Save(r.root, fresh); err != nil {
				r.logger.Printf("reconciler: save workspace.toml after clone: %v", err)
			}
		}
	}
	return nil
}
