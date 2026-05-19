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

// WorktreeList parses `git worktree list --porcelain` output. Works on either
// a bare repo or a regular checkout — git resolves to the same shared list.
func WorktreeList(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", repoPath, err)
	}
	return parsePorcelainWorktreeList(string(out)), nil
}

// parsePorcelainWorktreeList consumes the porcelain output of
// `git worktree list --porcelain` and returns one Worktree per
// blank-line-delimited record. Pure parser: no IO, fully unit-testable.
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

// applyWorktreeLine routes one porcelain line into the matching
// Worktree field. Unknown prefixes are silently ignored — git may
// add new attributes (e.g. `locked`, `prunable`) without breaking
// the parser.
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
