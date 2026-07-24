package sync

import (
	"context"
	"fmt"
	"path/filepath"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/conflict"
	"codeberg.org/kuchmenko/workspace/internal/git"
)

func (r *Runner) syncTOML() (bool, error) {
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlink: %w", err)
	}
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		return false, nil
	}
	origin, err := git.ConfiguredRemoteURL(repoRoot, "origin")
	if err != nil {
		return false, nil
	}
	remoteURL, err := git.ResolveRemoteURL(origin, repoRoot)
	if err != nil {
		return false, err
	}
	branch, err := workspaceBranch(repoRoot)
	if err != nil {
		return false, err
	}
	return r.syncTOMLContext(context.Background(), repoRoot, origin, remoteURL, branch)
}

func (r *Runner) syncTOMLContext(ctx context.Context, repoRoot, expectedOrigin, remoteURL, branch string) (bool, error) {
	relFile, err := r.prepareWorkspaceSync(ctx, repoRoot, expectedOrigin, branch)
	if err != nil {
		return false, err
	}
	originalHead := git.RevParse(repoRoot, "HEAD")
	if err := git.FetchURLContext(ctx, repoRoot, remoteURL); err != nil {
		r.logger.Printf("sync: fetch failed in %s: %v", repoRoot, err)
		return false, err
	}

	localDirty := !isClean(repoRoot, relFile)
	ahead, behind, hasOriginBranch := git.AheadBehindRemote(repoRoot, branch, "origin")
	if !hasOriginBranch {
		return false, nil
	}
	if !localDirty && ahead == 0 && behind == 0 {
		_ = r.clearTOMLConflicts()
		return false, nil
	}
	ahead, err = commitLocalWorkspaceChanges(ctx, repoRoot, relFile, ahead, localDirty)
	if err != nil {
		return false, err
	}
	ahead, behind, err = r.rebaseWorkspaceChanges(ctx, repoRoot, remoteURL, branch, ahead)
	if err != nil {
		return false, err
	}
	if err := r.pushWorkspaceChanges(ctx, repoRoot, remoteURL, branch, ahead, behind); err != nil {
		return false, err
	}
	newHead := git.RevParse(repoRoot, "HEAD")
	return newHead != originalHead, nil
}

func (r *Runner) prepareWorkspaceSync(ctx context.Context, repoRoot, expectedOrigin, expectedBranch string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return "", fmt.Errorf("resolve symlink: %w", err)
	}
	if currentRoot := findGitRoot(filepath.Dir(realPath)); currentRoot != repoRoot {
		return "", fmt.Errorf("workspace repository changed after preflight: got %q, want %q", currentRoot, repoRoot)
	}
	if err := exactWorkspaceOrigin(repoRoot, expectedOrigin); err != nil {
		return "", err
	}
	branch, err := workspaceBranch(repoRoot)
	if err != nil || branch != expectedBranch {
		return "", fmt.Errorf("workspace branch changed after preflight: got %q, want %q", branch, expectedBranch)
	}
	if err := ensureUnionMerge(repoRoot, realPath); err != nil {
		r.logger.Printf("sync: ensure union merge: %v", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		return "", err
	}
	return relFile, nil
}

func exactWorkspaceOrigin(repoRoot, expected string) error {
	actual, err := git.ConfiguredRemoteURL(repoRoot, "origin")
	if err != nil || !git.RemoteBindingExact(repoRoot, "origin", expected) {
		return fmt.Errorf("workspace origin changed after preflight: got %q, want %q", git.RedactRemote(actual), git.RedactRemote(expected))
	}
	return nil
}

func workspaceBranch(repoRoot string) (string, error) {
	branch, err := git.CurrentBranch(repoRoot)
	if err != nil || branch == "" {
		return "", fmt.Errorf("workspace repo is in detached HEAD")
	}
	return branch, nil
}

func commitLocalWorkspaceChanges(ctx context.Context, repoRoot, relFile string, ahead int, dirty bool) (int, error) {
	if !dirty {
		return ahead, nil
	}
	autoSyncMsg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", machineHostname())
	ahead, err := commitWorkspaceTOML(repoRoot, relFile, autoSyncMsg, ahead)
	if err != nil {
		return ahead, err
	}
	if err := ctx.Err(); err != nil {
		return ahead, err
	}
	return ahead, nil
}

func (r *Runner) rebaseWorkspaceChanges(ctx context.Context, repoRoot, remoteURL, branch string, ahead int) (int, int, error) {
	_, behind, _ := git.AheadBehindRemote(repoRoot, branch, "origin")
	if behind == 0 {
		return ahead, behind, nil
	}
	if err := git.RebaseURLBranchContext(ctx, repoRoot, remoteURL, branch); err != nil {
		if ctx.Err() != nil {
			return ahead, behind, ctx.Err()
		}
		r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
		return ahead, behind, err
	}
	_ = r.clearTOMLConflicts()
	ahead, behind, _ = git.AheadBehindRemote(repoRoot, branch, "origin")
	return ahead, behind, nil
}

func (r *Runner) pushWorkspaceChanges(ctx context.Context, repoRoot, remoteURL, branch string, ahead, behind int) error {
	if ahead == 0 && behind == 0 {
		return nil
	}
	return r.pushWorkspaceTOMLContext(ctx, repoRoot, remoteURL, branch)
}

func commitWorkspaceTOML(repoRoot, relFile, autoSyncMsg string, ahead int) (int, error) {
	if err := git.Add(repoRoot, relFile); err != nil {
		return ahead, fmt.Errorf("git add: %w", err)
	}
	headMsg, _ := git.LastCommitMessage(repoRoot)
	if ahead > 0 && headMsg == autoSyncMsg {
		if err := runIn(repoRoot, "git", "diff", "--cached", "--quiet", "HEAD~1"); err == nil {
			if err := runIn(repoRoot, "git", "reset", "--mixed", "HEAD~1"); err != nil {
				return ahead, fmt.Errorf("drop empty held auto-sync: %w", err)
			}
			return ahead - 1, nil
		}
		if err := runIn(repoRoot, "git", "commit", "--amend", "--no-edit"); err != nil {
			return ahead, fmt.Errorf("git commit --amend: %w", err)
		}
		return ahead, nil
	}
	if err := git.Commit(repoRoot, autoSyncMsg); err != nil {
		return ahead, fmt.Errorf("git commit: %w", err)
	}
	return ahead + 1, nil
}

func (r *Runner) pushWorkspaceTOMLContext(ctx context.Context, repoRoot, remoteURL, branch string) error {
	if err := r.validateWorkspaceTOMLForPush(); err != nil {
		r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
		return err
	}
	if err := git.PushURLBranchContext(ctx, repoRoot, remoteURL, branch); err == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := git.RebaseURLBranchContext(ctx, repoRoot, remoteURL, branch); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
		return err
	}
	if err := git.PushURLBranchContext(ctx, repoRoot, remoteURL, branch); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.recordTOMLConflict(repoRoot, conflict.KindTOMLPushFailed, err)
		return err
	}
	return nil
}

func (r *Runner) validateWorkspaceTOMLForPush() error {
	_, err := config.Load(r.root)
	return err
}
