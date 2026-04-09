package agent

import (
	"os"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// Worktree is a single worktree of a project, loaded on demand.
type Worktree struct {
	Path   string
	Branch string
	IsMain bool
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
