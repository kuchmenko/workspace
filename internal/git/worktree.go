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
func WorktreeAddExisting(repoPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "--force", wtPath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --force %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
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
