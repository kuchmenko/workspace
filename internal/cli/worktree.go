package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worktree",
		Aliases: []string{"wt"},
		Short:   "Manage per-project worktrees (repo-native branch names)",
		Annotations: map[string]string{
			"capability": "worktree",
			"agent:when": "Manage per-feature worktrees under a bare+worktree project layout",
		},
	}
	cmd.AddCommand(
		newWorktreeAddCmd(),
		newWorktreeListCmd(),
		newWorktreeRmCmd(),
		newWorktreePushCmd(),
	)
	return cmd
}

// resolveProject looks up a project by name in the loaded workspace and
// resolves both its main worktree path and its bare repo path. Returns
// an error if the project is not migrated yet.
func resolveProject(name string) (config.Project, string, string, error) {
	proj, ok := ws.Projects[name]
	if !ok {
		return config.Project{}, "", "", fmt.Errorf("project %q not found in workspace.toml", name)
	}
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		return proj, mainPath, barePath, fmt.Errorf("project %q is not migrated yet (no %s); run `ws migrate %s`", name, filepath.Base(barePath), name)
	}
	return proj, mainPath, barePath, nil
}

// locateWorktreeForBranch finds the existing worktree directory whose
// HEAD points at `branch`. Returns "" when no such worktree exists.
// Used by `ws worktree rm` and `ws worktree push` to find the path
// independent of the directory-naming heuristic used by `ws worktree
// add` (which may have applied a `-<sha8>` collision suffix).
func locateWorktreeForBranch(barePath, branch string) string {
	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return ""
	}
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		if wt.Branch == branch {
			return wt.Path
		}
	}
	return ""
}

// validateBranchName asks git itself whether a branch name is valid.
// Centralized so add/push/list all reject malformed names with the
// canonical message instead of letting later git operations fail with
// noisier output.
func validateBranchName(branch string) error {
	cmd := exec.Command("git", "check-ref-format", "--branch", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		hint := strings.TrimSpace(string(out))
		if hint == "" {
			hint = "git check-ref-format rejected this name"
		}
		return fmt.Errorf("invalid branch name %q: %s", branch, hint)
	}
	return nil
}
