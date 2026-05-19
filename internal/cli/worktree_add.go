package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

func newWorktreeAddCmd() *cobra.Command {
	var fromBase string
	cmd := &cobra.Command{
		Use:   "add <project> <branch>",
		Short: "Create or attach a worktree for the named branch",
		Annotations: map[string]string{
			"capability": "worktree",
			"agent:when": "Start a new feature in an isolated worktree, or check out an existing local/remote branch",
		},
		Long: `Create a new worktree for <project> on the literal branch <branch>.

The branch name is taken verbatim — no prefix injection, no slug
rewrite — beyond what git check-ref-format accepts. The same command
covers three cases:

  1. Branch is new: created from --from (or project default_branch)
     and a fresh [[branches]] entry is recorded in workspace.toml.

  2. Branch exists on origin: fetched into the bare repo, the new
     worktree checks it out, upstream tracking wired automatically.

  3. Branch exists locally only (no remote): worktree attaches to the
     existing local branch. This is also the path that re-registers a
     legacy wt/<machine>/<topic> branch under the new schema —
     ws worktree add myapp wt/linux/legacy-foo will pick it up and
     give it [[branches]] metadata.

EXAMPLES

  # New feature branch from main:
  ws worktree add myapp feat/auth-refactor

  # Auto-detect existing remote branch:
  ws worktree add myapp feat/data-api

  # Re-register a legacy wt/<machine>/* worktree:
  ws worktree add myapp wt/linux/old-topic

  # Branch off a non-default base:
  ws worktree add myapp hotfix --from release/v2`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, branch := args[0], strings.TrimSpace(args[1])
			if branch == "" {
				return errors.New("branch must not be empty")
			}
			if err := validateBranchName(branch); err != nil {
				return err
			}

			machine, err := ensureMachineName()
			if err != nil {
				return err
			}

			proj, mainPath, barePath, err := resolveProject(projectName)
			if err != nil {
				return err
			}

			if !git.HasFetchRefspec(barePath) {
				_ = git.SetFetchRefspec(barePath)
			}

			_ = git.FetchRefspec(barePath, "origin", branch)
			localExists := git.HasBranch(barePath, branch)
			remoteExists := git.HasRemoteBranch(barePath, "origin", branch)

			if existingWtPath := locateWorktreeForBranch(barePath, branch); existingWtPath != "" {
				p := ws.Projects[projectName]
				changed, _ := p.ClaimBranch(branch, machine)
				if remoteExists && p.MarkPushed(branch, machine, time.Now()) {
					changed = true
				}
				if changed {
					ws.Projects[projectName] = p
					if err := saveWorkspace(); err != nil {
						return fmt.Errorf("registry update failed: %w", err)
					}
				}
				machines := strings.Join(p.LookupBranch(branch).Machines, ", ")
				fmt.Printf("re-registered existing worktree %s\n  branch: %s\n  registered in workspace.toml (machines=[%s])\n",
					existingWtPath, branch, machines)
				return nil
			}

			wtPath := layout.WorktreePathForBranch(mainPath, machine, branch)
			if _, err := os.Stat(wtPath); err == nil {
				return fmt.Errorf("worktree path already exists: %s", wtPath)
			}

			source := ""
			switch {
			case localExists:
				if fromBase != "" {
					fmt.Fprintf(os.Stderr, "warning: --from ignored: branch %s already exists locally\n", branch)
				}
				if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
					return err
				}
				if remoteExists {
					source = "fetched"
				} else {
					source = "local"
				}
			case remoteExists:
				if fromBase != "" {
					fmt.Fprintf(os.Stderr, "warning: --from ignored: branch %s already exists on origin\n", branch)
				}
				if err := git.WorktreeAdd(barePath, wtPath, branch, "origin/"+branch); err != nil {
					return err
				}
				source = "fetched"
			default:
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
			}
			if source != "" {
				_ = git.SetBranchUpstream(wtPath, branch, "origin")
			}

			p := ws.Projects[projectName]
			changed, _ := p.ClaimBranch(branch, machine)
			if source == "fetched" && p.MarkPushed(branch, machine, time.Now()) {
				changed = true
			}
			if changed {
				ws.Projects[projectName] = p
				if err := saveWorkspace(); err != nil {
					return fmt.Errorf("worktree created but workspace.toml save failed: %w", err)
				}
			}

			machines := strings.Join(p.LookupBranch(branch).Machines, ", ")

			fmt.Printf("created worktree %s\n", wtPath)
			switch source {
			case "fetched":
				fmt.Printf("  branch: %s (checked out existing remote)\n", branch)
			case "local":
				fmt.Printf("  branch: %s (attached to existing local branch)\n", branch)
			default:
				base := fromBase
				if base == "" {
					base = proj.DefaultBranch
				}
				fmt.Printf("  branch: %s\n  base:   %s\n", branch, base)
			}
			fmt.Printf("  registered in workspace.toml (machines=[%s])\n", machines)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromBase, "from", "", "base ref to create the new branch from (default: project default_branch).\nIgnored with a warning when the branch already exists on origin or locally.")
	return cmd
}
