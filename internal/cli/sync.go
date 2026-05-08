package cli

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run one reconciler tick in the foreground",
		Annotations: map[string]string{
			"capability": "sync",
			"agent:when": "Manually trigger a full sync cycle: push/pull workspace.toml, fetch all projects, ff-pull main worktrees, refresh last_active_*, surface branch-orphan",
		},
		Long: `Synchronize this workspace right now without waiting for the daemon.

Performs the same work as a single daemon tick: commits and pushes
workspace.toml changes, pulls remote workspace.toml changes, fetches every
active project's bare repo, fast-forwards the main worktree when safe,
refreshes last_active_* for branches with local-ahead commits, and detects
origin-deleted branches as branch-orphan conflicts.

Project branches are never auto-pushed by the reconciler — that's an
explicit user action via 'ws worktree push'.

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

// runSyncResolve drives the per-conflict picker. Loads the store,
// surfaces the empty-store message once, then hands off to
// resolveLoop for the iterate-until-done dance.
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
	return resolveLoop(store, conflicts)
}

// resolveLoop runs the print-pick-resolve cycle. Returns when the
// user quits or when the store empties out (different end-message
// for "started empty" vs "drained while resolving").
func resolveLoop(store *conflict.Store, conflicts []conflict.Conflict) error {
	for {
		if !pickAndResolve(store, conflicts) {
			return nil // user quit
		}
		next, err := store.List()
		if err != nil {
			return err
		}
		if len(next) == 0 {
			fmt.Println("\nall conflicts resolved")
			return nil
		}
		conflicts = next
	}
}

// pickAndResolve prints the menu, reads the selection, dispatches
// the resolver. Returns false when the user quits the picker; true
// to keep the loop running (including on invalid input — the loop
// re-prompts on the next iteration with a fresh List()).
func pickAndResolve(store *conflict.Store, conflicts []conflict.Conflict) bool {
	printConflictList(conflicts)
	idx, quit := readConflictChoice(len(conflicts))
	if quit {
		return false
	}
	if idx < 0 {
		return true // invalid input
	}
	applyConflictResolution(store, conflicts[idx])
	return true
}

// printConflictList prints the numbered menu of unresolved conflicts.
// Pure formatting; the format mirrors the original `ws sync resolve`
// output ([N] kind — project/branch  (timestamp)).
func printConflictList(conflicts []conflict.Conflict) {
	fmt.Printf("\n%d unresolved conflict(s):\n", len(conflicts))
	for i, c := range conflicts {
		fmt.Printf("  [%d] %s  (%s)\n", i+1, conflictListLabel(c), c.DetectedAt.Local().Format("2006-01-02 15:04"))
	}
	fmt.Print("\nselect (number, q to quit): ")
}

// conflictListLabel renders one conflict's menu label: kind, project,
// branch — falling back to "workspace.toml" when the conflict is not
// project-scoped (TOML merge / push failures).
func conflictListLabel(c conflict.Conflict) string {
	if c.Project == "" {
		return string(c.Kind) + " — workspace.toml"
	}
	label := string(c.Kind) + " — " + c.Project
	if c.Branch != "" {
		label += "/" + c.Branch
	}
	return label
}

// readConflictChoice reads the user's selection. Returns:
//
//   - (-1, true)  on q / empty / EOF — user wants to quit
//   - (-1, false) on invalid number — caller re-prompts
//   - (idx, false) on valid 1-based index — converted to 0-based
func readConflictChoice(max int) (idx int, quit bool) {
	var input string
	_, _ = fmt.Scanln(&input)
	if input == "q" || input == "" {
		return -1, true
	}
	var n int
	if _, err := fmt.Sscanf(input, "%d", &n); err != nil || n < 1 || n > max {
		fmt.Println("invalid selection")
		return -1, false
	}
	return n - 1, false
}

// applyConflictResolution dispatches one selected conflict and, on
// successful resolution, removes it from the store. Errors from the
// resolver and the store are logged to stderr but never propagated —
// the caller continues the picker loop either way.
func applyConflictResolution(store *conflict.Store, c conflict.Conflict) {
	resolved, err := handleConflict(c)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	if !resolved {
		return
	}
	if err := store.Remove(c.ID); err != nil {
		fmt.Printf("warning: could not clear conflict: %v\n", err)
	}
}
