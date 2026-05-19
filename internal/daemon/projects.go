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
		if err := config.Save(r.root, ws); err != nil {
			r.logger.Printf("reconciler: save workspace.toml after metadata refresh: %v", err)
		}
	}
}

func (r *Reconciler) syncProject(name string, proj *config.Project, machine string, touched *bool) error {
	mainPath := filepath.Join(r.root, proj.Path)
	barePath := layout.BarePath(mainPath)

	bareMissing := false
	mainMissing := false
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		bareMissing = true
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		mainMissing = true
	}

	if bareMissing && mainMissing {
		if !r.autoBootstrap || !proj.SyncEnabled() {
			return nil
		}
		return r.autoCloneMissing(name, *proj)
	}

	if bareMissing {
		r.recordProjectConflict(name, "", conflict.KindNeedsMigration, fmt.Sprintf("plain checkout at %s", mainPath))
		return nil
	}

	if !git.HasFetchRefspec(barePath) {
		if err := git.SetFetchRefspec(barePath); err != nil {
			r.logger.Printf("reconciler: %s: repair fetch refspec: %v", name, err)
		}
	}

	if err := git.Fetch(barePath); err != nil {
		return err
	}

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

		isMain := wt.Path == mainPath

		if git.HasIndexLock(wt.Path) {
			continue
		}

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

		if machine != "" && proj.LookupBranch(wt.Branch) != nil {
			ahead, _, has := git.AheadBehind(wt.Path, wt.Branch)
			if has && ahead > 0 {
				if proj.TouchActive(wt.Branch, machine, time.Now()) {
					*touched = true
				}
			}
		}
	}

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

func (r *Reconciler) autoCloneMissing(name string, proj config.Project) error {
	r.logger.Printf("reconciler: auto-clone %s from %s", name, proj.Remote)

	res, err := clone.CloneIntoLayout(r.root, name, &proj, clone.Options{
		Logf: r.logger.Printf,
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

			return nil
		default:
			r.recordProjectConflict(name, "", conflict.KindCloneFailed, err.Error())
			return err
		}
	}

	r.logger.Printf("reconciler: cloned %s → %s (default_branch=%s)", name, res.BarePath, res.DefaultBranch)

	_ = r.clearProjectConflict(name, "", conflict.KindCloneFailed)
	_ = r.clearProjectConflict(name, "", conflict.KindNeedsBootstrap)

	if proj.DefaultBranch != "" {
		fresh, err := config.Load(r.root)
		if err != nil {
			r.logger.Printf("reconciler: reload workspace.toml after clone: %v", err)
			return nil
		}
		stored, ok := fresh.Projects[name]
		if !ok {
			return nil
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
