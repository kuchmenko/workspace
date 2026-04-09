package agent

import (
	"fmt"
	"os"

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
	if customBranch != "" {
		branch = customBranch
	} else {
		branch = layout.BranchName(machine, topic)
	}

	wtPath := layout.WorktreePath(p.Path, machine, topic)
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

	// Auto-push handling would require loading workspace.toml and saving
	// back — skipped for now, the daemon auto-pushes wt/<machine>/* by
	// default. Custom branches need explicit ws worktree promote.
	_ = autoPush

	return &WorktreeResult{Path: wtPath, Branch: branch}, nil
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
