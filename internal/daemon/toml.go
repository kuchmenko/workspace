package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
)

func (r *Reconciler) syncTOML() (bool, error) {
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlink: %w", err)
	}
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		return false, nil
	}
	if !git.HasRemote(repoRoot) {
		return false, nil
	}

	if err := ensureUnionMerge(repoRoot, realPath); err != nil {
		r.logger.Printf("reconciler: ensureUnionMerge: %v", err)
	}

	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		return false, err
	}

	originalHead := git.RevParse(repoRoot, "HEAD")

	if err := git.Fetch(repoRoot); err != nil {
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

	if !localDirty && ahead == 0 && behind == 0 {
		_ = r.clearTOMLConflicts()
		return false, nil
	}

	autoSyncMsg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", machineHostname())
	if localDirty {
		if err := git.Add(repoRoot, relFile); err != nil {
			return false, fmt.Errorf("git add: %w", err)
		}
		headMsg, _ := git.LastCommitMessage(repoRoot)
		if ahead > 0 && headMsg == autoSyncMsg {
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

	_, behind, _ = git.AheadBehind(repoRoot, branch)

	if behind > 0 {
		if err := runIn(repoRoot, "git", "pull", "--rebase"); err != nil {
			r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
			return false, err
		}
		_ = r.clearTOMLConflicts()
	}

	if ahead > 0 || behind > 0 {
		if r.shouldHoldPush(repoRoot, autoSyncMsg, ahead) {
			r.logger.Printf("reconciler: %s holding auto-sync commit for amend (cooldown %s)", repoRoot, r.pushCooldown)
		} else if err := git.Push(repoRoot); err != nil {
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
