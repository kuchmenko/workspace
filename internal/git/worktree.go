package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Worktree struct {
	Path     string
	HEAD     string
	Branch   string
	Bare     bool
	Detached bool
}

func WorktreeAdd(repoPath, wtPath, branch, createFromBase string) error {
	return worktreeAddContext(context.Background(), repoPath, wtPath, branch, createFromBase)
}

func worktreeAddContext(ctx context.Context, repoPath, wtPath, branch, createFromBase string) error {
	args := []string{"-C", repoPath, "worktree", "add"}
	if createFromBase != "" {
		args = append(args, "-b", branch, wtPath, createFromBase)
	} else {
		args = append(args, wtPath, branch)
	}
	out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return commandError(ctx, fmt.Sprintf("git worktree add %s in %s", wtPath, repoPath), string(out), err)
	}
	return nil
}

func WorktreeAddNoCheckout(repoPath, wtPath, branch string) error {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "add", "--no-checkout", wtPath, branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --no-checkout %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeRepair(repoPath string) error {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "repair").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree repair in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeRemove(repoPath, wtPath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove %s: %s", wtPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeList(repoPath string) ([]Worktree, error) {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", repoPath, err)
	}
	return parsePorcelainWorktreeList(string(out)), nil
}

func CommitTimes(repoPath string, commits []string) (map[string]time.Time, error) {
	result := make(map[string]time.Time, len(commits))
	if len(commits) == 0 {
		return result, nil
	}
	args := []string{"-C", repoPath, "show", "--no-patch", "--format=%H%x00%cI"}
	args = append(args, commits...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git show commit times in %s: %w", repoPath, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		value, err := time.Parse(time.RFC3339, parts[1])
		if err == nil {
			result[parts[0]] = value
		}
	}
	return result, nil
}

func parsePorcelainWorktreeList(text string) []Worktree {
	var result []Worktree
	var current Worktree
	open := false
	flush := func() {
		if open {
			result = append(result, current)
		}
		current = Worktree{}
		open = false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		open = true
		applyWorktreeLine(&current, line)
	}
	flush()
	return result
}

func applyWorktreeLine(current *Worktree, line string) {
	switch {
	case strings.HasPrefix(line, "worktree "):
		current.Path = strings.TrimPrefix(line, "worktree ")
	case strings.HasPrefix(line, "HEAD "):
		current.HEAD = strings.TrimPrefix(line, "HEAD ")
	case strings.HasPrefix(line, "branch "):
		ref := strings.TrimPrefix(line, "branch ")
		current.Branch = strings.TrimPrefix(ref, "refs/heads/")
	case line == "bare":
		current.Bare = true
	case line == "detached":
		current.Detached = true
	}
}
