package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newWorktreePushCmd() *cobra.Command {
	var forceDirty bool
	cmd := &cobra.Command{
		Use:   "push <project> <branch>",
		Short: "Push the branch to origin and stamp last_active_* in workspace.toml",
		Annotations: map[string]string{
			"capability": "worktree",
			"agent:when": "Publish a worktree's branch to origin and update the registry's last_active_* fields",
		},
		Long: `Push <branch> to origin from its local worktree. Updates
last_active_machine and last_active_at in workspace.toml so other machines
see the activity. Refuses dirty worktrees unless --force-dirty is set, and
refuses branches that are not registered in [[branches]] (a sign of
out-of-band creation; the user should re-register via ws worktree add).`,
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
			proj, _, barePath, err := resolveProject(projectName)
			if err != nil {
				return err
			}

			if proj.LookupBranch(branch) == nil {
				return fmt.Errorf("branch %s has no [[branches]] entry in workspace.toml\n"+
					"  this is usually a sign of an out-of-band creation; either:\n"+
					"    - ws worktree add %s %s  (re-register; works for legacy wt/* too)\n"+
					"    - cd <wt> && git push    (skip metadata update)",
					branch, projectName, branch)
			}

			wtPath := locateWorktreeForBranch(barePath, branch)
			if wtPath == "" {
				return fmt.Errorf("no worktree on branch %s; create one first with ws worktree add %s %s", branch, projectName, branch)
			}
			if !forceDirty && git.IsDirty(wtPath) {
				return fmt.Errorf("worktree %s is dirty; commit or stash, or rerun with --force-dirty", wtPath)
			}

			fmt.Printf("pushing %s to origin\n", branch)
			if err := git.PushBranch(wtPath, branch); err != nil {
				return fmt.Errorf("git push: %w", err)
			}
			_ = git.SetBranchUpstream(wtPath, branch, "origin")

			p := ws.Projects[projectName]
			if p.MarkPushed(branch, machine, time.Now()) {
				ws.Projects[projectName] = p
				if err := saveWorkspace(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: push succeeded but workspace.toml save failed: %v\n", err)
				}
			}
			meta := p.LookupBranch(branch)
			if meta != nil {
				fmt.Printf("updated workspace.toml: last_pushed_machine=%s, last_pushed_at=%s\n",
					meta.LastPushedMachine, meta.LastPushedAt)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&forceDirty, "force-dirty", false, "push even if the worktree has uncommitted changes")
	return cmd
}
