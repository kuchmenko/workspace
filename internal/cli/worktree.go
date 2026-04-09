package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	cmd.AddCommand(newWorktreeNewCmd(), newWorktreeListCmd(), newWorktreeRmCmd(), newWorktreePromoteCmd())
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
	var (
		fromBase     string
		customBranch string
		autoPush     bool
		reclaim      bool
	)
	cmd := &cobra.Command{
		Use:   "new <project> <topic>",
		Short: "Create a new worktree on branch wt/<machine>/<topic>",
		Long: `Create a new worktree. By default the branch is named
wt/<machine>/<topic> and is auto-pushed by the daemon. Pass --branch to use
a custom, repository-native branch name (e.g. feat/fix-login); such branches
are NOT auto-pushed unless you also pass --auto-push, which opts the branch
into the project's autopush list in workspace.toml.`,
		Args: cobra.ExactArgs(2),
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

			customBranch = strings.TrimSpace(customBranch)
			if autoPush && customBranch == "" {
				return errors.New("--auto-push requires --branch; wt/<machine>/* branches are always auto-pushed")
			}

			var branch string
			var pathTopic string // what goes into the worktree directory name
			if customBranch != "" {
				branch = customBranch
				// When --branch is explicit, derive the directory name
				// from the slugified branch (e.g. feat/buddy → feat-buddy)
				// so the path reflects the actual branch, not the topic arg.
				pathTopic = layout.SlugifyBranch(customBranch)
			} else {
				branch = layout.BranchName(machine, topic)
				pathTopic = topic
			}
			wtPath := layout.WorktreePath(mainPath, machine, pathTopic)

			if _, err := os.Stat(wtPath); err == nil {
				return fmt.Errorf("worktree path already exists: %s", wtPath)
			}
			if git.HasBranch(barePath, branch) {
				return fmt.Errorf("branch %s already exists; pick a different name or remove it first", branch)
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

			autopushNote := ""
			if autoPush {
				p := ws.Projects[projectName]
				changed, err := p.ClaimAutopushBranch(branch, machine, reclaim)
				if err != nil {
					return fmt.Errorf("worktree created but autopush claim failed: %w", err)
				}
				if changed {
					ws.Projects[projectName] = p
					if err := saveWorkspace(); err != nil {
						return fmt.Errorf("worktree created but failed to record autopush opt-in: %w", err)
					}
				}
				autopushNote = fmt.Sprintf(" (auto-push enabled, owner: %s)", machine)
			}

			fmt.Printf("created worktree %s\n  branch: %s%s\n  base:   %s\n", wtPath, branch, autopushNote, base)
			if customBranch != "" && !autoPush {
				fmt.Println("  note:   branch is outside wt/<machine>/* — daemon will not auto-push it; add --auto-push to opt in")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromBase, "from", "", "base ref to branch from (default: project default_branch)")
	cmd.Flags().StringVar(&customBranch, "branch", "", "custom branch name (bypasses wt/<machine>/<topic>; excluded from auto-push unless --auto-push is also set)")
	cmd.Flags().BoolVar(&autoPush, "auto-push", false, "opt the custom --branch into the project's daemon auto-push whitelist")
	cmd.Flags().BoolVar(&reclaim, "reclaim", false, "with --auto-push, take ownership even if another machine already owns the branch")
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

// newWorktreePromoteCmd implements `ws worktree promote`. It renames a
// wt/<machine>/<topic> WIP branch into its final, repository-native name
// (resolved from project.branch_naming.pattern with {topic} substitution,
// or supplied via --name), moves the worktree directory to match the new
// name, deletes the stale remote ref that the daemon already pushed, and
// updates project.autopush so the daemon keeps pushing under the new name.
func newWorktreePromoteCmd() *cobra.Command {
	var (
		newName  string
		noPush   bool
		noRemote bool
		reclaim  bool
	)
	cmd := &cobra.Command{
		Use:   "promote <project> <topic>",
		Short: "Rename wt/<machine>/<topic> to its final branch name (e.g. feat/<topic>)",
		Long: `Promote a WIP worktree to its final, repository-native branch
name before opening a PR. The final name is taken from --name if given,
otherwise from project.branch_naming.pattern (with {topic} substituted).
The branch is renamed, the worktree directory is moved to match, the stale
wt/<machine>/<topic> ref on origin is deleted (if present), and the new
name is opted into the project's autopush list so the daemon keeps pushing
it under the new name.`,
		Args: cobra.ExactArgs(2),
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

			oldBranch := layout.BranchName(machine, topic)
			oldPath := layout.WorktreePath(mainPath, machine, topic)

			if _, err := os.Stat(oldPath); err != nil {
				return fmt.Errorf("worktree not found: %s (expected for topic %q)", oldPath, topic)
			}
			if !git.HasBranch(barePath, oldBranch) {
				return fmt.Errorf("branch %s does not exist in %s", oldBranch, barePath)
			}

			// Resolve the final branch name.
			finalName := strings.TrimSpace(newName)
			if finalName == "" {
				if proj.BranchNaming == nil || proj.BranchNaming.Pattern == "" {
					return fmt.Errorf("project %s has no branch_naming.pattern; pass --name <new-branch> explicitly", projectName)
				}
				finalName = strings.ReplaceAll(proj.BranchNaming.Pattern, "{topic}", topic)
			}
			if finalName == oldBranch {
				return fmt.Errorf("resolved branch name %q is identical to current %q — nothing to promote", finalName, oldBranch)
			}
			// Optional regex validation.
			if proj.BranchNaming != nil && proj.BranchNaming.Validate != "" {
				re, err := regexp.Compile(proj.BranchNaming.Validate)
				if err != nil {
					return fmt.Errorf("invalid branch_naming.validate regex for project %s: %w", projectName, err)
				}
				if !re.MatchString(finalName) {
					return fmt.Errorf("branch name %q does not match project pattern %s", finalName, proj.BranchNaming.Validate)
				}
			}
			if git.HasBranch(barePath, finalName) {
				return fmt.Errorf("branch %s already exists; pick a different --name", finalName)
			}

			// Safety: refuse if the worktree is mid-edit or dirty. The
			// user can commit/stash first; we never move a dirty tree.
			if git.HasIndexLock(oldPath) {
				return fmt.Errorf("worktree %s has an active index.lock; close editors/git processes and retry", oldPath)
			}
			if git.IsDirty(oldPath) {
				return fmt.Errorf("worktree %s is dirty; commit or stash before promoting", oldPath)
			}

			// Compute new path. We reuse WorktreeDirName but with the
			// final branch name as the "topic" component, so the dir
			// name reflects the new branch instead of the old wt topic.
			// Slashes in finalName are flattened by WorktreeDirName.
			newPath := filepath.Join(filepath.Dir(mainPath),
				layout.WorktreeDirName(filepath.Base(mainPath), machine, finalName))
			if _, err := os.Stat(newPath); err == nil {
				return fmt.Errorf("target worktree path already exists: %s", newPath)
			}

			// Step 1: move the worktree directory. Git updates its
			// worktrees/<name>/gitdir pointer atomically.
			if err := git.WorktreeMove(barePath, oldPath, newPath); err != nil {
				return fmt.Errorf("move worktree: %w", err)
			}

			// Step 2: rename the branch. On failure, roll back the move
			// so the user's filesystem state matches the branch state.
			if err := git.BranchRename(newPath, oldBranch, finalName); err != nil {
				if rbErr := git.WorktreeMove(barePath, newPath, oldPath); rbErr != nil {
					return fmt.Errorf("branch rename failed (%v); rollback also failed (%v) — worktree now at %s on branch %s", err, rbErr, newPath, oldBranch)
				}
				return fmt.Errorf("branch rename: %w", err)
			}

			// Step 3: update workspace.toml — release any stale entry
			// for the old wt/* name and claim ownership of the new
			// branch on this machine. Reclaim handles the rare case
			// where another machine had already claimed the same
			// final name (e.g. parallel promotes that haven't synced).
			p := ws.Projects[projectName]
			p.ReleaseAutopushBranch(oldBranch)
			if _, err := p.ClaimAutopushBranch(finalName, machine, reclaim); err != nil {
				// Roll back filesystem + branch rename so the user
				// can retry with --reclaim cleanly.
				_ = git.BranchRename(newPath, finalName, oldBranch)
				_ = git.WorktreeMove(barePath, newPath, oldPath)
				return err
			}
			ws.Projects[projectName] = p
			if err := saveWorkspace(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: branch renamed and worktree moved, but workspace.toml update failed: %v\n", err)
			}

			// Step 4: delete the stale remote ref. Best-effort — not
			// fatal if the daemon never got around to pushing it.
			if !noRemote {
				if err := git.DeleteRemoteBranch(barePath, oldBranch); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not delete remote ref %s: %v\n", oldBranch, err)
				}
			}

			// Step 5: push the renamed branch so reviewers can find it.
			// The daemon would eventually do this anyway via the new
			// autopush entry, but doing it inline gives the user a
			// synchronous confirmation and a predictable PR-ready state.
			if !noPush {
				if err := git.PushBranch(newPath, finalName); err != nil {
					fmt.Fprintf(os.Stderr, "warning: push of %s failed: %v (daemon will retry)\n", finalName, err)
				}
			}

			fmt.Printf("promoted worktree\n  branch: %s → %s\n  path:   %s → %s\n",
				oldBranch, finalName, oldPath, newPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&newName, "name", "", "explicit final branch name (overrides project.branch_naming.pattern)")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "skip pushing the renamed branch (daemon will still pick it up)")
	cmd.Flags().BoolVar(&noRemote, "no-remote-delete", false, "skip deleting the stale wt/<machine>/<topic> ref on origin")
	cmd.Flags().BoolVar(&reclaim, "reclaim", false, "take ownership of the final branch even if another machine already owns it")
	return cmd
}
