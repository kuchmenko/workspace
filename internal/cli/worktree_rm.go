package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newWorktreeRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <project> <branch>",
		Short: "Remove a worktree (refuses if dirty or unpushed unless --force)",
		Annotations: map[string]string{
			"capability":   "worktree",
			"agent:when":   "Remove a worktree after its branch has been merged or is no longer needed",
			"agent:safety": "Refuses if dirty or has unpushed commits unless --force. Does not delete the branch on origin.",
		},
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, branch := args[0], strings.TrimSpace(args[1])
			if branch == "" {
				return errors.New("branch must not be empty")
			}
			machine, err := ensureMachineName()
			if err != nil {
				return err
			}
			_, mainPath, barePath, err := resolveProject(projectName)
			if err != nil {
				return err
			}
			wtPath := locateWorktreeForBranch(barePath, branch)
			if wtPath == "" {
				return fmt.Errorf("no worktree on branch %s in project %s", branch, projectName)
			}

			if wtPath == mainPath {
				return fmt.Errorf("refusing to remove main worktree of %s (branch %s is checked out at %s)", projectName, branch, mainPath)
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

			p := ws.Projects[projectName]
			if changed, _ := p.ReleaseBranch(branch, machine); changed {
				ws.Projects[projectName] = p
				if err := saveWorkspace(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: worktree removed but workspace.toml save failed: %v\n", err)
				}
			}
			fmt.Printf("removed worktree %s\n", wtPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if dirty or has unpushed commits")
	return cmd
}
