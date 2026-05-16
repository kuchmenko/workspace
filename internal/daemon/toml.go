package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"

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
	// committed state.
	if localDirty {
		if err := git.Add(repoRoot, relFile); err != nil {
			return false, fmt.Errorf("git add: %w", err)
		}
		host := machineHostname()
		msg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", host)
		if err := git.Commit(repoRoot, msg); err != nil {
			return false, fmt.Errorf("git commit: %w", err)
		}
		ahead++
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

	// Push if anything to push.
	if ahead > 0 || behind > 0 {
		if err := git.Push(repoRoot); err != nil {
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
