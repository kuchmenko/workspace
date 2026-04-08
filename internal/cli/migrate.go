package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/migrate"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var (
		all   bool
		check bool
		wip   bool
	)

	cmd := &cobra.Command{
		Use:   "migrate [project]",
		Short: "Convert plain checkouts into the bare+worktree layout",
		Long: `Convert one or all active projects from a plain 'git clone' checkout
into the worktree layout (bare repo as a sibling, main worktree in place).

Examples:
  ws migrate myapp           migrate one project
  ws migrate --all           migrate every active project
  ws migrate --check         report which projects need migration
  ws migrate myapp --wip     auto-snapshot dirty changes to a WIP branch

Stash entries and detached HEAD always abort. Dirty working trees abort
unless --wip is given, in which case the changes are committed to a
wt/<machine>/migration-wip-<timestamp> branch and exposed as a sibling
worktree after the migration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runMigrateCheck(args)
			}
			if !all && len(args) != 1 {
				return errors.New("specify a project name or use --all")
			}
			if all && len(args) > 0 {
				return errors.New("cannot combine --all with a project name")
			}

			machine, err := ensureMachineName()
			if err != nil {
				return err
			}

			var targets []string
			if all {
				for name, p := range ws.Projects {
					if p.Status == config.StatusActive {
						targets = append(targets, name)
					}
				}
				sort.Strings(targets)
			} else {
				targets = args
			}

			opts := migrate.Options{
				WIP:                 wip,
				Machine:             machine,
				PromptDefaultBranch: promptDefaultBranchStdin,
				Logf: func(format string, a ...interface{}) {
					fmt.Printf("  "+format+"\n", a...)
				},
			}

			anyMigrated := false
			anyFailed := false
			migratedCount := 0
			skippedMissing := 0
			skippedAlready := 0
			for _, name := range targets {
				proj, ok := ws.Projects[name]
				if !ok {
					fmt.Printf("  skip   %s: not in workspace.toml\n", name)
					continue
				}
				if proj.Status != config.StatusActive {
					fmt.Printf("  skip   %s: status=%s\n", name, proj.Status)
					continue
				}
				// Pre-check state. For --all we want missing projects to be a
				// soft skip (the registry travels between machines, so it's
				// normal for some projects to not exist locally yet).
				if all {
					switch migrate.Check(wsRoot, name, proj).State {
					case "missing":
						fmt.Printf("  skip   %s: not cloned on this machine\n", name)
						skippedMissing++
						continue
					case "migrated":
						fmt.Printf("  skip   %s: already migrated\n", name)
						skippedAlready++
						continue
					case "not-a-repo":
						fmt.Printf("  skip   %s: path exists but is not a git repo\n", name)
						continue
					}
				}
				res, err := migrate.MigrateProject(wsRoot, name, &proj, opts)
				if err != nil {
					if errors.Is(err, migrate.ErrAlreadyMigrated) {
						fmt.Printf("  skip   %s: already migrated\n", name)
						skippedAlready++
						continue
					}
					fmt.Printf("  error  %s: %v\n", name, err)
					anyFailed = true
					continue
				}
				ws.Projects[name] = proj // proj.DefaultBranch was filled in
				anyMigrated = true
				migratedCount++
				fmt.Printf("  done   %s → %s (%d branches preserved", name, res.BarePath, res.BranchesPushed)
				if len(res.HooksMigrated) > 0 {
					fmt.Printf(", %d hooks", len(res.HooksMigrated))
				}
				if res.WIPWorktree != "" {
					fmt.Printf(", WIP at %s", res.WIPWorktree)
				}
				fmt.Println(")")
			}

			if anyMigrated {
				if err := saveWorkspace(); err != nil {
					return err
				}
			}
			if all {
				if migratedCount == 0 && !anyFailed {
					if skippedMissing > 0 || skippedAlready > 0 {
						fmt.Printf("Nothing to migrate (%d already migrated, %d not cloned on this machine).\n", skippedAlready, skippedMissing)
					} else {
						fmt.Println("No active projects to migrate.")
					}
				} else {
					fmt.Printf("Migrated %d project(s); skipped %d already migrated, %d not cloned locally.\n", migratedCount, skippedAlready, skippedMissing)
				}
			}
			if anyFailed {
				return errors.New("some migrations failed")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "migrate every active project")
	cmd.Flags().BoolVar(&check, "check", false, "report state without making changes")
	cmd.Flags().BoolVar(&wip, "wip", false, "snapshot dirty trees to a WIP branch instead of aborting")
	return cmd
}

func runMigrateCheck(args []string) error {
	names := args
	if len(names) == 0 {
		for n := range ws.Projects {
			names = append(names, n)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		fmt.Println("No projects registered in workspace.toml.")
		return nil
	}
	for _, name := range names {
		proj, ok := ws.Projects[name]
		if !ok {
			fmt.Printf("  ?      %s: not in workspace.toml\n", name)
			continue
		}
		r := migrate.Check(wsRoot, name, proj)
		var note []string
		if r.HasStash {
			note = append(note, "stash present")
		}
		if r.IsDirty {
			note = append(note, "dirty")
		}
		if r.Detached {
			note = append(note, "detached HEAD")
		}
		if r.HooksFound > 0 {
			note = append(note, fmt.Sprintf("%d hooks", r.HooksFound))
		}
		if r.Branch != "" {
			note = append(note, "branch="+r.Branch)
		}
		extra := ""
		if len(note) > 0 {
			extra = " [" + strings.Join(note, ", ") + "]"
		}
		fmt.Printf("  %-15s %s%s\n", r.State, name, extra)
	}
	return nil
}

// ensureMachineName loads the machine config, prompting once if absent.
// Returns the sanitized machine name to use for branch namespacing.
func ensureMachineName() (string, error) {
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return "", err
	}
	if mc.MachineName != "" {
		return mc.MachineName, nil
	}
	def := config.DefaultMachineName()
	fmt.Printf("First-time setup: pick a short machine name for branch namespacing.\n")
	fmt.Printf("This will appear in branch names like wt/<machine>/<topic>.\n")
	fmt.Printf("Suggested: %s\n", def)
	fmt.Printf("Machine name [%s]: ", def)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	clean := config.SanitizeMachineName(line)
	if clean == "" {
		return "", errors.New("machine name cannot be empty after sanitization")
	}
	mc.MachineName = clean
	if err := config.SaveMachineConfig(mc); err != nil {
		return "", err
	}
	fmt.Printf("Saved machine name: %s\n", clean)
	return clean, nil
}

func promptDefaultBranchStdin(project string, candidates []string) (string, error) {
	fmt.Printf("Default branch for %s could not be auto-detected.\n", project)
	if len(candidates) > 0 {
		fmt.Printf("Candidates found locally: %s\n", strings.Join(candidates, ", "))
	}
	def := ""
	if len(candidates) == 1 {
		def = candidates[0]
	} else if len(candidates) > 0 {
		def = candidates[0]
	}
	if def != "" {
		fmt.Printf("Default branch [%s]: ", def)
	} else {
		fmt.Printf("Default branch: ")
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	if line == "" {
		return "", errors.New("no branch entered")
	}
	return line, nil
}
