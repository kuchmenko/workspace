package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// Worktree describes one entry from `git worktree list --porcelain`.
type Worktree struct {
	Path     string // absolute path to the worktree directory
	HEAD     string // commit SHA HEAD points to
	Branch   string // short branch name; empty if detached
	Bare     bool   // true for the bare repo entry itself
	Detached bool
}

// WorktreeAdd creates a new worktree at `wtPath` checking out `branch`.
// If `createFromBase` is non-empty, the branch is created from that base ref;
// otherwise the branch must already exist.
func WorktreeAdd(repoPath, wtPath, branch, createFromBase string) error {
	args := []string{"-C", repoPath, "worktree", "add"}
	if createFromBase != "" {
		args = append(args, "-b", branch, wtPath, createFromBase)
	} else {
		args = append(args, wtPath, branch)
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeAddExisting attaches an existing directory as a worktree for the
// named branch. Used by migration: after we move the original checkout's
// .git aside, we run this to make the existing files become a real worktree.
// Requires --force because the target path already contains files.
//
// DEPRECATED: kept for backwards compatibility. Modern git refuses to attach
// a worktree to a non-empty existing directory even with --force; use
// WorktreeAddNoCheckout + manual pointer swap instead. See migrate.go for
// the working strategy.
func WorktreeAddExisting(repoPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "--force", wtPath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --force %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeAddNoCheckout creates a worktree at wtPath checked out on branch,
// but skips writing the working-tree files. The result is a directory
// containing only a .git pointer file (and the matching admin dir under
// repoPath/worktrees/<name>/). Used by migrate to materialize a worktree's
// metadata without overwriting the user's existing files.
//
// wtPath must NOT already exist — git enforces this even with --no-checkout.
// The migrate flow uses a sibling temp path and then moves the .git pointer
// file into the real (existing) main path.
func WorktreeAddNoCheckout(repoPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "--no-checkout", wtPath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --no-checkout %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeRepair tells git to update its worktree admin directory entries
// after their working trees have been moved. Used by migrate after we
// physically rename a freshly-created worktree's .git pointer file from a
// temp sibling into the real main path: without WorktreeRepair the bare
// repo's worktrees/<name>/gitdir still points at the temp location, which
// then gets pruned and silently breaks the worktree.
func WorktreeRepair(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "repair")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree repair in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeRemove removes a worktree. With force=false, git refuses if the
// worktree has uncommitted changes.
func WorktreeRemove(repoPath, wtPath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove %s: %s", wtPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeMove renames a worktree directory. Wraps `git worktree move`,
// which updates the bare repo's worktrees/<name>/gitdir entry and the
// worktree's .git pointer file atomically. Refuses if the worktree is
// dirty or locked.
//
// repoPath can be either the bare repo or any worktree — `git worktree
// move` accepts both, since they share the same admin dir.
func WorktreeMove(repoPath, oldPath, newPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "move", oldPath, newPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree move %s → %s: %s", oldPath, newPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// BranchRename renames a local branch. Wraps `git branch -m`. The
// rename preserves reflog. Fails if the new name already exists.
func BranchRename(repoPath, oldName, newName string) error {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-m", oldName, newName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -m %s %s in %s: %s", oldName, newName, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteRemoteBranch deletes a branch on origin via `git push origin
// :<ref>`. Used by `ws worktree promote` to remove the stale
// wt/<machine>/<topic> ref after renaming. Non-fatal for callers when
// the ref does not exist remotely — they should log and continue.
func DeleteRemoteBranch(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "push", "origin", "--delete", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push origin --delete %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeList parses `git worktree list --porcelain` output. Works on either
// a bare repo or a regular checkout — git resolves to the same shared list.
func WorktreeList(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", repoPath, err)
	}

	var (
		result []Worktree
		cur    Worktree
		open   bool
	)
	flush := func() {
		if open {
			result = append(result, cur)
		}
		cur = Worktree{}
		open = false
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		open = true
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		}
	}
	flush()
	return result, nil
}
