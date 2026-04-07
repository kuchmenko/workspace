package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		Short:   "Manage per-project worktrees (wt/<machine>/<topic>)",
	}
	cmd.AddCommand(newWorktreeNewCmd(), newWorktreeListCmd(), newWorktreeRmCmd())
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

func newWorktreeNewCmd() *cobra.Command {
	var fromBase string
	cmd := &cobra.Command{
		Use:   "new <project> <topic>",
		Short: "Create a new worktree on branch wt/<machine>/<topic>",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, topic := args[0], args[1]
			topic = strings.TrimSpace(topic)
			if topic == "" {
				return errors.New("topic must not be empty")
			}

			machine, err := ensureMachineName()
			if err != nil {
				return err
			}

			proj, mainPath, barePath, err := resolveProject(projectName)
			if err != nil {
				return err
			}

			branch := layout.BranchName(machine, topic)
			wtPath := layout.WorktreePath(mainPath, machine, topic)

			if _, err := os.Stat(wtPath); err == nil {
				return fmt.Errorf("worktree path already exists: %s", wtPath)
			}
			if git.HasBranch(barePath, branch) {
				return fmt.Errorf("branch %s already exists; pick a different topic or remove it first", branch)
			}

			base := fromBase
			if base == "" {
				base = proj.DefaultBranch
			}
			if base == "" {
				return fmt.Errorf("project %s has no default_branch and --from was not given", projectName)
			}

			if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
				return err
			}
			fmt.Printf("created worktree %s\n  branch: %s\n  base:   %s\n", wtPath, branch, base)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromBase, "from", "", "base ref to branch from (default: project default_branch)")
	return cmd
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [project]",
		Short: "List worktrees across projects",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			machine, _ := config.LoadMachineConfig()
			myMachine := ""
			if machine != nil {
				myMachine = machine.MachineName
			}

			var names []string
			if len(args) == 1 {
				names = []string{args[0]}
			} else {
				for n, p := range ws.Projects {
					if p.Status == config.StatusActive {
						names = append(names, n)
					}
				}
				sort.Strings(names)
			}

			fmt.Printf("%-20s %-40s %-30s %s\n", "PROJECT", "WORKTREE", "BRANCH", "STATE")
			for _, name := range names {
				proj, ok := ws.Projects[name]
				if !ok {
					continue
				}
				mainPath := filepath.Join(wsRoot, proj.Path)
				barePath := layout.BarePath(mainPath)
				if _, err := os.Stat(barePath); err != nil {
					fmt.Printf("%-20s %s\n", name, "(not migrated)")
					continue
				}
				wts, err := git.WorktreeList(barePath)
				if err != nil {
					fmt.Printf("%-20s ERROR %v\n", name, err)
					continue
				}
				for _, wt := range wts {
					if wt.Bare {
						continue
					}
					rel, _ := filepath.Rel(wsRoot, wt.Path)
					if rel == "" {
						rel = wt.Path
					}
					branchLabel := wt.Branch
					if wt.Detached {
						branchLabel = "(detached)"
					}
					state := worktreeStateString(wt, myMachine, proj.DefaultBranch)
					fmt.Printf("%-20s %-40s %-30s %s\n", name, rel, branchLabel, state)
				}
			}
			return nil
		},
	}
}

func worktreeStateString(wt git.Worktree, myMachine, defaultBranch string) string {
	parts := []string{}
	if git.IsDirty(wt.Path) {
		parts = append(parts, "DIRTY")
	} else {
		parts = append(parts, "clean")
	}
	if wt.Branch != "" {
		ahead, behind, has := git.AheadBehind(wt.Path, wt.Branch)
		if has {
			parts = append(parts, fmt.Sprintf("↑%d ↓%d", ahead, behind))
		} else {
			parts = append(parts, "no upstream")
		}
	}
	owner := "shared"
	if wt.Branch == defaultBranch {
		owner = "main"
	} else if myMachine != "" && strings.HasPrefix(wt.Branch, layout.BranchPrefix(myMachine)) {
		owner = "mine"
	} else if strings.HasPrefix(wt.Branch, "wt/") {
		owner = "remote"
	}
	parts = append(parts, owner)
	return strings.Join(parts, ", ")
}

func newWorktreeRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <project> <topic>",
		Short: "Remove a worktree (refuses if dirty or unpushed unless --force)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, topic := args[0], args[1]
			machine, err := ensureMachineName()
			if err != nil {
				return err
			}
			_, mainPath, barePath, err := resolveProject(projectName)
			if err != nil {
				return err
			}
			wtPath := layout.WorktreePath(mainPath, machine, topic)
			branch := layout.BranchName(machine, topic)

			if _, err := os.Stat(wtPath); os.IsNotExist(err) {
				return fmt.Errorf("worktree not found: %s", wtPath)
			}

			if !force {
				if git.IsDirty(wtPath) {
					return fmt.Errorf("worktree %s is dirty; commit/stash or use --force", wtPath)
				}
				ahead, _, has := git.AheadBehind(wtPath, branch)
				if has && ahead > 0 {
					return fmt.Errorf("branch %s has %d unpushed commits; push or use --force", branch, ahead)
				}
			}

			if err := git.WorktreeRemove(barePath, wtPath, force); err != nil {
				return err
			}
			fmt.Printf("removed worktree %s\n", wtPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if dirty or has unpushed commits")
	return cmd
}
