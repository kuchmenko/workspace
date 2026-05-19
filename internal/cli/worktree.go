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
