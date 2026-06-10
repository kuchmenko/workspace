package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
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

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [project]",
		Short: "List worktrees across projects",
		Annotations: map[string]string{
			"capability": "worktree",
			"agent:when": "List all worktrees across projects with branch, dirty/clean state, and ownership info",
		},
		Args: cobra.MaximumNArgs(1),
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
