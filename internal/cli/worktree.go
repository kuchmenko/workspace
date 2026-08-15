package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worktree",
		Aliases: []string{"wt"},
		Short:   "Manage per-project worktrees (repo-native branch names)",
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
		return config.Project{}, "", "", fmt.Errorf("project %q not found in workspace registry", name)
	}
	mainPath, err := layout.ProjectPath(wsRoot, proj.Path)
	if err != nil {
		return proj, "", "", fmt.Errorf("project %q: %w", name, err)
	}
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

func newWorktreeAddCmd() *cobra.Command {
	var fromBase string
	cmd := &cobra.Command{
		Use:         "add <project> <branch>",
		Short:       "Create or attach a worktree for the named branch",
		Annotations: agentAnnotations("worktree-add", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectRead, "text", "0,1"),
		Long: `Create a new worktree for <project> on the literal branch <branch>.

The branch name is taken verbatim — no prefix injection, no slug
rewrite — beyond what git check-ref-format accepts. The same command
covers three cases:

  1. Branch is new: created from --from (or project default_branch)
     and fresh branch metadata is recorded in the workspace registry.

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
			machine, err := ensureMachineName()
			if err != nil {
				return err
			}
			result, err := repo.AddWorktree(repo.WorktreeAddOptions{
				WorkspaceRoot: wsRoot,
				Workspace:     ws,
				Save:          saveWorkspaceState,
				Project:       projectName,
				Branch:        branch,
				Machine:       machine,
				From:          fromBase,
			})
			if err != nil {
				return err
			}
			if result.Warning != "" {
				fmt.Fprintf(os.Stderr, "warning: %s\n", result.Warning)
			}
			machines := strings.Join(result.Machines, ", ")
			if result.ReRegistered {
				fmt.Printf("re-registered existing worktree %s\n  branch: %s\n  registered in workspace registry (machines=[%s])\n",
					result.Path, branch, machines)
				return nil
			}
			fmt.Printf("created worktree %s\n", result.Path)
			switch result.Source {
			case "fetched":
				fmt.Printf("  branch: %s (checked out existing remote)\n", branch)
			case "local":
				fmt.Printf("  branch: %s (attached to existing local branch)\n", branch)
			default:
				fmt.Printf("  branch: %s\n  base:   %s\n", branch, result.Base)
			}
			fmt.Printf("  registered in workspace registry (machines=[%s])\n", machines)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromBase, "from", "", "base ref to create the new branch from (default: project default_branch).\nIgnored with a warning when the branch already exists on origin or locally.")
	return cmd
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "list [project]",
		Short:       "List worktrees across projects",
		Annotations: agentAnnotations("worktree-list", AgentInteractionNone, AgentApprovalNone, AgentEffectNone, AgentEffectNone, "table", "0,1"),
		Args:        cobra.MaximumNArgs(1),
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

			fmt.Printf("%-20s %-50s %-30s %s\n", "PROJECT", "WORKTREE", "BRANCH", "STATE")
			for _, name := range names {
				proj, ok := ws.Projects[name]
				if !ok {
					continue
				}
				mainPath, err := layout.ProjectPath(wsRoot, proj.Path)
				if err != nil {
					fmt.Printf("%-20s ERROR %v\n", name, err)
					continue
				}
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
					state := worktreeStateString(&proj, wt, myMachine, proj.DefaultBranch)
					fmt.Printf("%-20s %-50s %-30s %s\n", name, rel, branchLabel, state)
				}
			}
			return nil
		},
	}
}

func worktreeStateString(proj *config.Project, wt git.Worktree, myMachine, defaultBranch string) string {
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
	switch {
	case wt.Branch == defaultBranch:
		owner = "main"
	case strings.HasPrefix(wt.Branch, "wt/"):
		owner = "legacy-wt"
	default:
		if meta := proj.LookupBranch(wt.Branch); meta != nil {
			myMine := false
			others := []string{}
			for _, m := range meta.Machines {
				if m == myMachine {
					myMine = true
					continue
				}
				others = append(others, m)
			}
			if myMine && len(others) == 0 {
				owner = "mine"
			} else if myMine {
				owner = "shared with " + strings.Join(others, ", ")
			} else if len(others) > 0 {
				owner = "remote (" + strings.Join(others, ", ") + ")"
			}
			if meta.LastActiveMachine != "" && meta.LastActiveAt != "" {
				if t, err := time.Parse(time.RFC3339, meta.LastActiveAt); err == nil {
					owner += fmt.Sprintf(" (last: %s %s)", meta.LastActiveMachine, t.Format("2006-01-02"))
				}
			}
		}
	}
	parts = append(parts, owner)
	return strings.Join(parts, ", ")
}

func newWorktreePushCmd() *cobra.Command {
	var forceDirty bool
	cmd := &cobra.Command{
		Use:         "push <project> <branch>",
		Short:       "Push the branch to origin and stamp activity in the workspace registry",
		Annotations: agentAnnotations("worktree-push", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectWrite, "text", "0,1"),
		Long: `Push <branch> to origin from its local worktree. Updates
last_active_machine and last_active_at in the workspace registry so other machines
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
				return fmt.Errorf("branch %s has no [[branches]] entry in the workspace registry\n"+
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
					fmt.Fprintf(os.Stderr, "warning: push succeeded but workspace registry save failed: %v\n", err)
				}
			}
			meta := p.LookupBranch(branch)
			if meta != nil {
				fmt.Printf("updated workspace registry: last_pushed_machine=%s, last_pushed_at=%s\n",
					meta.LastPushedMachine, meta.LastPushedAt)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&forceDirty, "force-dirty", false, "push even if the worktree has uncommitted changes")
	return cmd
}

func newWorktreeRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:         "rm <project> <branch>",
		Short:       "Remove a worktree (refuses if dirty or unpushed unless --force)",
		Annotations: agentAnnotations("worktree-remove", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "text", "0,1"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, branch := args[0], strings.TrimSpace(args[1])
			machine, err := ensureMachineName()
			if err != nil {
				return err
			}
			_, _, barePath, resolveErr := resolveProject(projectName)
			if resolveErr != nil {
				return resolveErr
			}
			wtPath := locateWorktreeForBranch(barePath, branch)
			result, err := repo.RemoveWorktree(repo.WorktreeRemoveOptions{WorkspaceRoot: wsRoot, Workspace: ws, Save: saveWorkspaceState, Project: projectName, Branch: branch, Machine: machine, Force: force})
			if result.Removed {
				fmt.Printf("removed worktree %s\n", wtPath)
			}
			if err != nil {
				return err
			}
			if !result.Removed && result.MetadataReleased {
				fmt.Printf("released stale workspace registry ownership for %s\n", branch)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if dirty or has unpushed commits")
	return cmd
}
