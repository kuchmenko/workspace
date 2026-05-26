package cli

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/layout"
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

func resolveLoop(store *daemon.ConflictStore, conflicts []daemon.Conflict) error {
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

func pickAndResolve(store *daemon.ConflictStore, conflicts []daemon.Conflict) bool {
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

func printConflictList(conflicts []daemon.Conflict) {
	fmt.Printf("\n%d unresolved conflict(s):\n", len(conflicts))
	for i, c := range conflicts {
		fmt.Printf("  [%d] %s  (%s)\n", i+1, conflictListLabel(c), c.DetectedAt.Local().Format("2006-01-02 15:04"))
	}
	fmt.Print("\nselect (number, q to quit): ")
}

func conflictListLabel(c daemon.Conflict) string {
	if c.Project == "" {
		return string(c.Kind) + " — workspace.toml"
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

func applyConflictResolution(store *daemon.ConflictStore, c daemon.Conflict) {
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

func openConflictStore() (*daemon.ConflictStore, error) {
	return daemon.OpenConflictStore()
}

func handleConflict(c daemon.Conflict) (bool, error) {
	printConflictHeader(c)
	switch c.Kind {
	case daemon.KindTOMLMerge, daemon.KindTOMLPushFailed:
		return resolveTOMLConflict(c)
	case daemon.KindMainDivergence:
		return resolveProjectConflict(c)
	case daemon.KindBranchDuplicate:
		return resolveBranchDuplicate(c)
	case daemon.KindBranchOrphan:
		return resolveBranchOrphan(c)
	case daemon.KindNeedsMigration:
		return resolveNeedsMigration(c)
	case daemon.KindNeedsBootstrap:
		return resolveNeedsBootstrap(c)
	case daemon.KindPathBlocked:
		return resolvePathBlocked(c)
	case daemon.KindCloneFailed:
		return resolveCloneFailed(c)
	}
	fmt.Println("Unknown conflict kind. Press enter to continue.")
	_ = readLine()
	return false, nil
}

func printConflictHeader(c daemon.Conflict) {
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

func resolveNeedsMigration(c daemon.Conflict) (bool, error) {
	fmt.Println("This project needs migration. Run:")
	fmt.Printf("  ws migrate %s\n", c.Project)
	fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
	_ = readLine()
	return false, nil
}

func resolveNeedsBootstrap(c daemon.Conflict) (bool, error) {
	fmt.Println("This project needs to be cloned on this machine. Run:")
	fmt.Printf("  ws bootstrap %s\n", c.Project)
	fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
	_ = readLine()
	return false, nil
}

func resolvePathBlocked(c daemon.Conflict) (bool, error) {
	fmt.Println("The target path is already taken by something else.")
	if len(c.Details) > 0 {
		fmt.Printf("  details: %s\n", string(c.Details))
	}
	fmt.Println("Move or remove the blocking path, then re-run `ws sync`.")
	fmt.Println("Press enter to continue.")
	_ = readLine()
	return false, nil
}

func resolveCloneFailed(c daemon.Conflict) (bool, error) {
	fmt.Println("Clone failed for this project.")
	if len(c.Details) > 0 {
		fmt.Printf("  details: %s\n", string(c.Details))
	}
	fmt.Println("Check credentials and network, then re-run `ws bootstrap` or `ws sync`.")
	fmt.Println("Press enter to continue.")
	_ = readLine()
	return false, nil
}

func resolveBranchDuplicate(c daemon.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"e", "open workspace.toml in $EDITOR — pick which entry to keep",
			func() (bool, error) { return editWorkspaceForDuplicate(c) }},
	}, "k", "")
}

func editWorkspaceForDuplicate(c daemon.Conflict) (bool, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	if err := runInTerm(c.Workspace, editor, "workspace.toml"); err != nil {
		return false, err
	}
	fmt.Println("returned from editor. Mark conflict resolved? (y/N)")
	if strings.EqualFold(strings.TrimSpace(readLine()), "y") {
		return true, nil
	}
	return false, nil
}

func resolveBranchOrphan(c daemon.Conflict) (bool, error) {
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

func findOrphanWorktree(c daemon.Conflict) string {
	if ws == nil || c.Project == "" {
		return ""
	}
	proj, ok := ws.Projects[c.Project]
	if !ok {
		return ""
	}
	barePath := layout.BarePath(filepath.Join(c.Workspace, proj.Path))
	return locateWorktreeForBranch(barePath, c.Branch)
}

func dropOrphanEntry(c daemon.Conflict, wtPath string) (bool, error) {
	if wtPath != "" {
		fmt.Printf("Run: ws worktree rm %s %s --force\n", c.Project, c.Branch)
		fmt.Println("Press enter once removed (or 's' to skip).")
		if strings.TrimSpace(readLine()) == "s" {
			return false, nil
		}
	}
	if err := removeBranchFromWorkspace(c.Project, c.Branch); err != nil {
		fmt.Printf("warning: workspace.toml save failed: %v\n", err)
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
	return saveWorkspace()
}

func keepOrphanLocal(c daemon.Conflict) (bool, error) {
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
	if err := saveWorkspace(); err != nil {
		fmt.Printf("warning: workspace.toml save failed: %v\n", err)
		return false, nil
	}
	return true, nil
}

type promptAction struct {
	key, label string
	handler    func() (done bool, err error)
}

func runPromptLoop(actions []promptAction, skipKeys ...string) (bool, error) {
	for {
		printPromptMenu(actions, skipKeys)
		choice := strings.TrimSpace(readLine())
		if isSkipKey(choice, skipKeys) {
			return false, nil
		}
		done, err := dispatchPromptChoice(choice, actions)
		if err != nil || done {
			return done, err
		}
	}
}

func printPromptMenu(actions []promptAction, skipKeys []string) {
	fmt.Println("Options:")
	for _, a := range actions {
		fmt.Printf("  (%s) %s\n", a.key, a.label)
	}
	for _, k := range skipKeys {
		if k == "" {
			continue
		}
		fmt.Printf("  (%s) skip — leave for later\n", k)
	}
	fmt.Print("> ")
}

func isSkipKey(choice string, skipKeys []string) bool {
	for _, k := range skipKeys {
		if choice == k {
			return true
		}
	}
	return false
}

func dispatchPromptChoice(choice string, actions []promptAction) (bool, error) {
	for _, a := range actions {
		if a.key == choice {
			return a.handler()
		}
	}
	return false, nil
}

func resolveTOMLConflict(c daemon.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"s", "open shell in workspace repo — fix manually, exit shell to return",
			func() (bool, error) { return shellAndConfirm(c.Workspace) }},
		{"d", "show git status",
			func() (bool, error) { return false, runInTerm(c.Workspace, "git", "status") }},
	}, "k", "")
}

func shellAndConfirm(dir string) (bool, error) {
	if err := openShell(dir); err != nil {
		return false, err
	}
	fmt.Println("returned from shell. Mark conflict resolved? (y/N)")
	if strings.EqualFold(strings.TrimSpace(readLine()), "y") {
		return true, nil
	}
	return false, nil
}

func resolveProjectConflict(c daemon.Conflict) (bool, error) {
	wtPath, err := findWorktreePath(c.Workspace, c.Project, c.Branch)
	if err != nil {
		fmt.Printf("warning: could not locate worktree: %v\n", err)
	}
	return runPromptLoop([]promptAction{
		{"l", "git log local..remote (remote-only commits)",
			func() (bool, error) { runDivergenceLog(wtPath, c.Branch+"..@{u}"); return false, nil }},
		{"r", "git log remote..local (local-only commits)",
			func() (bool, error) { runDivergenceLog(wtPath, "@{u}.."+c.Branch); return false, nil }},
		{"o", "open shell in worktree — fix manually",
			func() (bool, error) { return openShellAtWorktree(wtPath) }},
	}, "k", "")
}

func openShellAtWorktree(wtPath string) (bool, error) {
	if wtPath == "" {
		fmt.Println("no worktree path; cannot open shell")
		return false, nil
	}
	return shellAndConfirm(wtPath)
}

func runDivergenceLog(wtPath, gitRange string) {
	if wtPath == "" {
		return
	}
	_ = runInTerm(wtPath, "git", "log", "--oneline", gitRange)
}

func findWorktreePath(workspace, project, branch string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace not loaded")
	}
	proj, ok := ws.Projects[project]
	if !ok {
		return "", fmt.Errorf("project %s not in workspace.toml", project)
	}
	mainPath := workspace + string(os.PathSeparator) + proj.Path

	_ = branch
	return mainPath, nil
}

func openShell(dir string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runInTerm(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func readLine() string {
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
