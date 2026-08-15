package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "sync",
		Short:       "Synchronize registered project repositories",
		Annotations: agentAnnotations("sync", AgentInteractionConditional, AgentApprovalRequired, AgentEffectWrite, AgentEffectWrite, "text", "0,1,130"),
		Args:        cobra.NoArgs,
		Long: `Synchronize this workspace right now.

Before changing anything, builds a fresh plan and probes every unique
project and mirror endpoint noninteractively. In a terminal,
review sources and targets, optionally exclude them for this run, and choose
only verified known-provider HTTPS-to-SSH origin conversions. With redirected
input or output, every endpoint must pass or no changes are made.

After confirmation, clones or fetches selected active projects, pushes
selected mirrors, fast-forwards eligible main
worktrees, refreshes last_active_* for local-ahead registered branches, and
detects origin-deleted branches as branch-orphan conflicts.

Project branches are never pushed to origin by 'ws sync'; that's an explicit
user action via 'ws worktree push'.

Conflicts are recorded to ~/.local/state/ws/conflicts.json.
Use 'ws sync resolve' to inspect and act on them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd.Context(), wsRoot, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.AddCommand(newSyncResolveCmd())
	return cmd
}

func newSyncResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve",
		Short: "Inspect and act on unresolved sync conflicts",
		Args:  cobra.NoArgs,
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
	return resolveLoop(store, conflicts)
}

func resolveLoop(store *conflict.Store, conflicts []conflict.Conflict) error {
	for {
		if !pickAndResolve(store, conflicts) {
			return nil
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

func pickAndResolve(store *conflict.Store, conflicts []conflict.Conflict) bool {
	printConflictList(conflicts)
	idx, quit := readConflictChoice(len(conflicts))
	if quit {
		return false
	}
	if idx < 0 {
		return true
	}
	applyConflictResolution(store, conflicts[idx])
	return true
}

func printConflictList(conflicts []conflict.Conflict) {
	fmt.Printf("\n%d unresolved conflict(s):\n", len(conflicts))
	for i, c := range conflicts {
		fmt.Printf("  [%d] %s  (%s)\n", i+1, conflictListLabel(c), c.DetectedAt.Local().Format("2006-01-02 15:04"))
	}
	fmt.Print("\nselect (number, q to quit): ")
}

func conflictListLabel(c conflict.Conflict) string {
	if c.Project == "" {
		return string(c.Kind) + " — workspace"
	}
	label := string(c.Kind) + " — " + c.Project
	if c.Branch != "" {
		label += "/" + c.Branch
	}
	return label
}

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

func openConflictStore() (*conflict.Store, error) {
	return conflict.Open()
}

func handleConflict(c conflict.Conflict) (bool, error) {
	printConflictHeader(c)
	switch c.Kind {
	case conflict.KindMainDivergence:
		return resolveProjectConflict(c)
	case conflict.KindBranchDuplicate:
		return resolveBranchDuplicate(c)
	case conflict.KindBranchOrphan:
		return resolveBranchOrphan(c)
	case conflict.KindNeedsMigration:
		return resolveNeedsMigration(c)
	case conflict.KindNeedsBootstrap:
		return resolveNeedsBootstrap(c)
	case conflict.KindPathBlocked:
		return resolvePathBlocked(c)
	case conflict.KindCloneFailed:
		return resolveCloneFailed(c)
	case conflict.KindMirrorPushFailed:
		return resolveMirrorPushFailed(c)
	}
	fmt.Println("Unknown conflict kind. Press enter to continue.")
	_ = readLine()
	return false, nil
}

func printConflictHeader(c conflict.Conflict) {
	fmt.Println()
	fmt.Printf("Conflict: %s\n", c.Kind)
	if c.Project != "" {
		fmt.Printf("  project: %s\n", c.Project)
	}
	if c.Branch != "" {
		fmt.Printf("  branch:  %s\n", c.Branch)
	}
	fmt.Printf("  workspace: %s\n", c.Workspace)
	if len(c.Details) > 0 {
		fmt.Printf("  details: %s\n", string(c.Details))
	}
	fmt.Println()
}

func resolveNeedsMigration(c conflict.Conflict) (bool, error) {
	fmt.Println("This project needs migration. Run:")
	fmt.Printf("  ws migrate %s\n", c.Project)
	fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
	_ = readLine()
	return false, nil
}

func resolveNeedsBootstrap(c conflict.Conflict) (bool, error) {
	fmt.Println("This project needs to be cloned on this machine. Run:")
	fmt.Printf("  ws bootstrap %s\n", c.Project)
	fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
	_ = readLine()
	return false, nil
}

func resolvePathBlocked(c conflict.Conflict) (bool, error) {
	fmt.Println("The target path is already taken by something else.")
	if len(c.Details) > 0 {
		fmt.Printf("  details: %s\n", string(c.Details))
	}
	fmt.Println("Move or remove the blocking path, then re-run `ws sync`.")
	fmt.Println("Press enter to continue.")
	_ = readLine()
	return false, nil
}

func resolveCloneFailed(c conflict.Conflict) (bool, error) {
	fmt.Println("Clone failed for this project.")
	if len(c.Details) > 0 {
		fmt.Printf("  details: %s\n", string(c.Details))
	}
	fmt.Println("Check credentials and network, then re-run `ws bootstrap` or `ws sync`.")
	fmt.Println("Press enter to continue.")
	_ = readLine()
	return false, nil
}

func resolveBranchDuplicate(c conflict.Conflict) (bool, error) {
	fmt.Println("Resolve the duplicate branch metadata through the relevant worktree command, then re-run `ws sync`.")
	fmt.Println("Press enter to continue.")
	_ = readLine()
	return false, nil
}

func resolveMirrorPushFailed(c conflict.Conflict) (bool, error) {
	var details struct {
		Mirror string `json:"mirror"`
		URL    string `json:"url"`
	}
	_ = json.Unmarshal(c.Details, &details)
	if details.Mirror == "" {
		details.Mirror = c.Branch
	}
	fmt.Printf("mirror: %s → %s\n", details.Mirror, details.URL)

	barePath := findProjectBare(c)
	if barePath == "" {
		fmt.Println("cannot locate bare repo for this project; fix manually and re-run `ws sync`.")
		fmt.Println("Press enter to continue.")
		_ = readLine()
		return false, nil
	}
	return runPromptLoop([]promptAction{
		{"r", "retry push to the mirror now",
			func() (bool, error) {
				if err := git.PushMirror(barePath, details.Mirror); err != nil {
					fmt.Printf("push failed: %v\n", err)
					return false, nil
				}
				fmt.Println("mirror push succeeded")
				return true, nil
			}},
		{"o", "open shell in bare repo — inspect/fix manually",
			func() (bool, error) { return shellAndConfirm(barePath) }},
	}, "k", "")
}

func findProjectBare(c conflict.Conflict) string {
	if ws == nil || c.Project == "" {
		return ""
	}
	proj, ok := ws.Projects[c.Project]
	if !ok {
		return ""
	}
	mainPath, err := layout.ProjectPath(c.Workspace, proj.Path)
	if err != nil {
		return ""
	}
	return layout.BarePath(mainPath)
}

func resolveBranchOrphan(c conflict.Conflict) (bool, error) {
	wtPath := findOrphanWorktree(c)
	dropLabel := "drop [[branches]] entry — no local worktree on this machine"
	if wtPath != "" {
		dropLabel = "drop [[branches]] entry (run ws worktree rm first)"
	}
	return runPromptLoop([]promptAction{
		{"d", dropLabel,
			func() (bool, error) { return dropOrphanEntry(c, wtPath) }},
		{"k", "keep local — clear last_pushed_* so the orphan check stops firing",
			func() (bool, error) { return keepOrphanLocal(c) }},
	}, "s", "")
}

func findOrphanWorktree(c conflict.Conflict) string {
	barePath := findProjectBare(c)
	if barePath == "" {
		return ""
	}
	return locateWorktreeForBranch(barePath, c.Branch)
}

func dropOrphanEntry(c conflict.Conflict, wtPath string) (bool, error) {
	if wtPath != "" {
		fmt.Printf("Run: ws worktree rm %s %s --force\n", c.Project, c.Branch)
		fmt.Println("Press enter once removed (or 's' to skip).")
		if strings.TrimSpace(readLine()) == "s" {
			return false, nil
		}
	}
	if err := removeBranchFromWorkspace(c.Project, c.Branch); err != nil {
		fmt.Printf("warning: workspace registry save failed: %v\n", err)
		return false, nil
	}
	return true, nil
}

func removeBranchFromWorkspace(project, branch string) error {
	if ws == nil || project == "" || branch == "" {
		return nil
	}
	proj := ws.Projects[project]
	if !proj.RemoveBranch(branch) {
		return nil
	}
	ws.Projects[project] = proj
	return saveWorkspaceState(ws)
}

func keepOrphanLocal(c conflict.Conflict) (bool, error) {
	if ws == nil || c.Project == "" || c.Branch == "" {
		return true, nil
	}
	proj := ws.Projects[c.Project]
	meta := proj.LookupBranch(c.Branch)
	if meta == nil {
		return true, nil
	}
	if meta.LastPushedAt == "" && meta.LastPushedMachine == "" {
		return true, nil
	}
	meta.LastPushedAt = ""
	meta.LastPushedMachine = ""
	ws.Projects[c.Project] = proj
	if err := saveWorkspaceState(ws); err != nil {
		fmt.Printf("warning: workspace registry save failed: %v\n", err)
		return false, nil
	}
	return true, nil
}
