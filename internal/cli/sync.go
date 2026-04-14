package cli

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run one reconciler tick in the foreground",
		Annotations: map[string]string{
			"capability": "sync",
			"agent:when": "Manually trigger a full sync cycle: push/pull workspace.toml, fetch all projects, ff-pull main worktrees, push owned wt/* branches",
		},
		Long: `Synchronize this workspace right now without waiting for the daemon.

Performs the same work as a single daemon tick: commits and pushes
workspace.toml changes, pulls remote workspace.toml changes, fetches every
active project's bare repo, fast-forwards the main worktree when safe, and
pushes any local wt/<machine>/* branches that are ahead.

Conflicts and skipped operations are recorded to ~/.local/state/ws/conflicts.json.
Use 'ws sync resolve' to inspect and act on them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(os.Stdout, "", 0)
			r := daemon.NewReconciler(wsRoot, 5*time.Minute, logger)
			r.Tick()
			return nil
		},
	}
	cmd.AddCommand(newSyncResolveCmd())
	return cmd
}

func newSyncResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve",
		Short: "Inspect and act on unresolved sync conflicts",
		Annotations: map[string]string{
			"capability":   "sync",
			"agent:when":   "View and resolve sync conflicts (branch divergence, merge failures, etc.)",
			"agent:safety": "Interactive prompt — opens a shell for the user to resolve manually. Never auto-merges.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncResolve()
		},
	}
}

func runSyncResolve() error {
	store, err := openConflictStore()
	if err != nil {
		return err
	}
	conflicts, err := store.List()
	if err != nil {
		return err
	}
	if len(conflicts) == 0 {
		fmt.Println("no unresolved conflicts")
		return nil
	}

	for {
		fmt.Printf("\n%d unresolved conflict(s):\n", len(conflicts))
		for i, c := range conflicts {
			label := string(c.Kind)
			if c.Project != "" {
				label += " — " + c.Project
				if c.Branch != "" {
					label += "/" + c.Branch
				}
			} else {
				label += " — workspace.toml"
			}
			fmt.Printf("  [%d] %s  (%s)\n", i+1, label, c.DetectedAt.Local().Format("2006-01-02 15:04"))
		}
		fmt.Print("\nselect (number, q to quit): ")
		var input string
		_, _ = fmt.Scanln(&input)
		if input == "q" || input == "" {
			return nil
		}
		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 1 || idx > len(conflicts) {
			fmt.Println("invalid selection")
			continue
		}
		removed, err := handleConflict(conflicts[idx-1])
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		if removed {
			if err := store.Remove(conflicts[idx-1].ID); err != nil {
				fmt.Printf("warning: could not clear conflict: %v\n", err)
			}
		}
		// Refresh
		conflicts, err = store.List()
		if err != nil {
			return err
		}
		if len(conflicts) == 0 {
			fmt.Println("\nall conflicts resolved")
			return nil
		}
	}
}
