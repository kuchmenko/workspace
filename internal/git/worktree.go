package git

import (
	"fmt"
	"os/exec"
	"strings"
)

type Worktree struct {
	Path     string
	HEAD     string
	Branch   string
	Bare     bool
	Detached bool
}

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

func WorktreeAddNoCheckout(repoPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "--no-checkout", wtPath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --no-checkout %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeRepair(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "repair")
	out, err := cmd.CombinedOutput()
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
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove %s: %s", wtPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeList(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", repoPath, err)
	}
	return parsePorcelainWorktreeList(string(out)), nil
}

func parsePorcelainWorktreeList(text string) []Worktree {
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
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		open = true
		applyWorktreeLine(&cur, line)
	}
	flush()
	return result
}

func applyWorktreeLine(cur *Worktree, line string) {
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
