package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// Worktree is a single worktree of a project, loaded on demand.
type Worktree struct {
	Path   string
	Branch string
	IsMain bool
}

// WorktreeResult is returned after successful worktree creation.
type WorktreeResult struct {
	Path   string
	Branch string
}

// CreateWorktree creates a new worktree for the given project. Mirrors
// the logic of `ws worktree new` but callable from the TUI.
//
// topic:        required — the worktree topic name (e.g. "fix-login")
// customBranch: optional — overrides the default wt/<machine>/<topic>
// autoPush:     whether to add the branch to daemon auto-push
// CreateWorktree creates a new worktree for the given project.
//
// When customBranch is set, the topic is IGNORED — the worktree directory
// name is derived from the slugified branch name instead:
//
//	branch "feat/buddy" → dir "myapp-wt-linux-feat-buddy"
//	branch "" + topic "buddy" → dir "myapp-wt-linux-buddy"  (default wt/<machine>/buddy)
//
// This matches the convention in CLAUDE.md: worktree dirs flatten
// slashes to dashes, and the branch name is the source of truth when
// explicitly provided.
func CreateWorktree(p *Project, topic, customBranch string, autoPush bool) (*WorktreeResult, error) {
	barePath := layout.BarePath(p.Path)
	if _, err := os.Stat(barePath); err != nil {
		return nil, fmt.Errorf("project not migrated (no bare repo at %s)", barePath)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	var branch string
	var pathTopic string // what goes into the worktree directory name
	if customBranch != "" {
		branch = customBranch
		// Derive path topic from branch: feat/buddy → feat-buddy
		pathTopic = layout.SlugifyBranch(customBranch)
	} else {
		branch = layout.BranchName(machine, topic)
		pathTopic = topic
	}

	wtPath := layout.WorktreePath(p.Path, machine, pathTopic)
	if _, err := os.Stat(wtPath); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", wtPath)
	}
	if git.HasBranch(barePath, branch) {
		return nil, fmt.Errorf("branch %s already exists", branch)
	}

	base := p.DefaultBranch
	if base == "" {
		base = "main"
	}

	if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}

	_ = autoPush
	return &WorktreeResult{Path: wtPath, Branch: branch}, nil
}


// DeleteWorktree removes a worktree. Refuses if it's the main worktree.
func DeleteWorktree(mainPath, wtPath string, force bool) error {
	if wtPath == mainPath {
		return fmt.Errorf("cannot delete main worktree")
	}
	barePath := layout.BarePath(mainPath)
	return git.WorktreeRemove(barePath, wtPath, force)
}

// PromoteWorktree renames a wt/<machine>/<topic> branch to a
// repository-native branch name (e.g. feat/fix-login) AND moves the
// worktree directory to match, so the on-disk name reflects the new
// branch.
//
// Steps (matching the CLI `ws worktree promote` flow):
//  1. Move worktree directory: old-path → new-path (git worktree move)
//  2. Rename branch: old-branch → new-branch (git branch -m)
//  3. If branch rename fails, roll back the directory move.
//
// Does NOT handle: autopush, remote ref delete, push. The daemon
// picks these up on the next tick via workspace.toml.
func PromoteWorktree(mainPath string, wt Worktree, newBranch string) error {
	if wt.IsMain {
		return fmt.Errorf("cannot promote main worktree")
	}
	if newBranch == "" {
		return fmt.Errorf("new branch name required")
	}

	barePath := layout.BarePath(mainPath)
	if !git.HasBranch(barePath, wt.Branch) {
		return fmt.Errorf("branch %s does not exist", wt.Branch)
	}
	if git.HasBranch(barePath, newBranch) {
		return fmt.Errorf("branch %s already exists", newBranch)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	// New directory path derived from slugified new branch name.
	newPath := filepath.Join(
		filepath.Dir(mainPath),
		layout.WorktreeDirName(filepath.Base(mainPath), machine, layout.SlugifyBranch(newBranch)),
	)
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("target path already exists: %s", newPath)
	}

	// Step 1: move worktree directory.
	if err := git.WorktreeMove(barePath, wt.Path, newPath); err != nil {
		return fmt.Errorf("move worktree: %w", err)
	}

	// Step 2: rename branch inside the new worktree path (same as CLI
	// promote). On failure, roll back the move.
	if err := git.BranchRename(newPath, wt.Branch, newBranch); err != nil {
		// Roll back directory move.
		_ = git.WorktreeMove(barePath, newPath, wt.Path)
		return fmt.Errorf("rename branch: %w", err)
	}

	// Step 3: best-effort delete the old remote ref.
	_ = git.DeleteRemoteBranch(barePath, wt.Branch)

	return nil
}

// worktreeDisplayName returns a human-readable short name for a worktree.
// For main it's "main". For wt/<machine>/<topic> it extracts the topic.
// For custom branches it shows the branch. For long directory-derived
// names it extracts the meaningful suffix.
func worktreeDisplayName(wt Worktree) string {
	if wt.IsMain {
		return "main"
	}
	// Try to extract topic from wt/<machine>/<topic> branch name.
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return parts[2] // topic
		}
	}
	// Custom branch — show as-is.
	if wt.Branch != "" {
		return wt.Branch
	}
	// Fallback to directory base name.
	return filepath.Base(wt.Path)
}

// LoadWorktrees returns the worktrees for a project. Requires the
// project to be migrated (bare repo exists).
func LoadWorktrees(mainPath string) []Worktree {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		// Not migrated — return just the main path.
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true}}
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true}}
	}

	var result []Worktree
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		result = append(result, Worktree{
			Path:   wt.Path,
			Branch: wt.Branch,
			IsMain: wt.Path == mainPath,
		})
	}
	return result
}
