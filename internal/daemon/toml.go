package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
)

// syncTOML implements the decision matrix from the design proposal §6.2.
// Returns (tomlChangedOnDisk, error).
func (r *Reconciler) syncTOML() (bool, error) {
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlink: %w", err)
	}
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		return false, nil // not in a git repo, nothing to sync
	}
	if !git.HasRemote(repoRoot) {
		return false, nil
	}

	// Ensure the .gitattributes union-merge driver is in place. This makes
	// most concurrent edits to workspace.toml merge cleanly without manual
	// intervention. Best-effort: failure to write is logged but not fatal.
	if err := ensureUnionMerge(repoRoot, realPath); err != nil {
		r.logger.Printf("reconciler: ensureUnionMerge: %v", err)
	}

	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		return false, err
	}

	// Capture original HEAD so we can detect whether pull changed the file.
	originalHead := git.RevParse(repoRoot, "HEAD")

	if err := git.Fetch(repoRoot); err != nil {
		// Network failures here are common and not actionable; log and skip.
		r.logger.Printf("reconciler: fetch failed in %s: %v", repoRoot, err)
		return false, nil
	}

	localDirty := !isClean(repoRoot, relFile)
	branch, _ := git.CurrentBranch(repoRoot)
	if branch == "" {
		return false, fmt.Errorf("workspace repo is in detached HEAD")
	}
	ahead, behind, hasUpstream := git.AheadBehind(repoRoot, branch)
	if !hasUpstream {
		return false, nil
	}

	// Fast path: nothing to do.
	if !localDirty && ahead == 0 && behind == 0 {
		_ = r.clearTOMLConflicts()
		return false, nil
	}

	// Commit dirty changes first so the rest of the matrix only deals with
	// committed state. When HEAD is already an unpushed auto-sync commit from
	// this host, amend into it instead of stacking another one — see the
	// pushCooldown design note in reconciler.go.
	autoSyncMsg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", machineHostname())
	if localDirty {
		if err := git.Add(repoRoot, relFile); err != nil {
			return false, fmt.Errorf("git add: %w", err)
		}
		headMsg, _ := git.LastCommitMessage(repoRoot)
		if ahead > 0 && headMsg == autoSyncMsg {
			// If the staged tree now matches HEAD's parent, the held commit's
			// net change has been undone (e.g. a favorite toggled on then off
			// inside the cooldown). git refuses an amend that produces an
			// empty diff vs parent; without this branch we'd return an error
			// every subsequent tick and leave workspace.toml staged forever.
			// Drop the held commit instead — the right history outcome is
			// "no commit at all".
			if err := runIn(repoRoot, "git", "diff", "--cached", "--quiet", "HEAD~1"); err == nil {
				if err := runIn(repoRoot, "git", "reset", "--mixed", "HEAD~1"); err != nil {
					return false, fmt.Errorf("drop empty held auto-sync: %w", err)
				}
				ahead--
			} else if err := runIn(repoRoot, "git", "commit", "--amend", "--no-edit"); err != nil {
				return false, fmt.Errorf("git commit --amend: %w", err)
			}
		} else {
			if err := git.Commit(repoRoot, autoSyncMsg); err != nil {
				return false, fmt.Errorf("git commit: %w", err)
			}
			ahead++
		}
	}

	// Re-evaluate behind in case fetch happened pre-commit.
	_, behind, _ = git.AheadBehind(repoRoot, branch)

	// If remote moved while we were committing, rebase before push.
	if behind > 0 {
		if err := runIn(repoRoot, "git", "pull", "--rebase"); err != nil {
			r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
			return false, err
		}
		_ = r.clearTOMLConflicts()
	}

	// Push if anything to push — unless the pushCooldown gate is holding our
	// auto-sync commit open for further amending. The held commit will be
	// pushed on a later tick once its age exceeds the cooldown, or sooner if
	// a non-auto-sync commit lands on top of it.
	if ahead > 0 || behind > 0 {
		if r.shouldHoldPush(repoRoot, autoSyncMsg, ahead) {
			r.logger.Printf("reconciler: %s holding auto-sync commit for amend (cooldown %s)", repoRoot, r.pushCooldown)
		} else if err := git.Push(repoRoot); err != nil {
			// One retry: fetch + rebase + push, mirror of the legacy syncer.
			if perr := runIn(repoRoot, "git", "pull", "--rebase"); perr != nil {
				r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, perr)
				return false, perr
			}
			if perr := git.Push(repoRoot); perr != nil {
				r.recordTOMLConflict(repoRoot, conflict.KindTOMLPushFailed, perr)
				return false, perr
			}
		}
	}

	newHead := git.RevParse(repoRoot, "HEAD")
	return newHead != originalHead, nil
}

// shouldHoldPush reports whether HEAD is our own auto-sync commit that is
// still young enough to absorb further amends. Zero pushCooldown disables
// the gate entirely (the historical behavior, kept for `ws sync`).
//
// The age check uses the author date — preserved by `git commit --amend
// --no-edit` — so continuous activity that keeps amending into the held
// commit cannot indefinitely defer the push. The committer date would
// refresh on every amend and silently turn the cooldown into "never push
// while busy", which is the failure mode this gate exists to prevent.
//
// The ahead==1 guard prevents the gate from withholding a user's manual
// commit that sits below the auto-sync: in that case `git push` would
// publish *both* commits, and the cooldown is only entitled to defer the
// auto-sync one. When ahead > 1 we always push.
func (r *Reconciler) shouldHoldPush(repoRoot, autoSyncMsg string, ahead int) bool {
	if r.pushCooldown <= 0 {
		return false
	}
	if ahead != 1 {
		return false
	}
	headMsg, _ := git.LastCommitMessage(repoRoot)
	if headMsg != autoSyncMsg {
		return false
	}
	t, err := git.LastCommitAuthorTime(repoRoot)
	if err != nil {
		return false
	}
	return time.Since(t) < r.pushCooldown
}

func (r *Reconciler) recordTOMLConflict(workspace string, kind conflict.Kind, cause error) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"error": cause.Error()})
	c := conflict.Conflict{
		Workspace: workspace,
		Kind:      kind,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("reconciler: record conflict: %v", err)
		return
	}
	if created {
		r.logger.Printf("reconciler: NEW conflict %s in %s: %v", kind, workspace, cause)
		conflict.NotifyNew(c)
	}
}

func (r *Reconciler) clearTOMLConflicts() error {
	if r.store == nil {
		return nil
	}
	for _, k := range []conflict.Kind{conflict.KindTOMLMerge, conflict.KindTOMLPushFailed} {
		_ = r.store.Clear(r.root, "", "", k)
	}
	return nil
}
