package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/layout"
)

// openConflictStore is a tiny shim around conflict.Open so the cli package
// doesn't need to import the package in two files.
func openConflictStore() (*conflict.Store, error) {
	return conflict.Open()
}

// handleConflict drives the prompt for one conflict. Returns (resolved, err)
// where resolved=true means the caller should clear the conflict from the
// store. The reconciler may also clear it on the next tick automatically;
// either path is fine.
func handleConflict(c conflict.Conflict) (bool, error) {
	printConflictHeader(c)
	switch c.Kind {
	case conflict.KindTOMLMerge, conflict.KindTOMLPushFailed:
		return resolveTOMLConflict(c)
	case conflict.KindMainDivergence:
		return resolveProjectConflict(c)
	case conflict.KindBranchDuplicate:
		return resolveBranchDuplicate(c)
	case conflict.KindBranchOrphan:
		return resolveBranchOrphan(c)
	case conflict.KindNeedsMigration:
		return resolveNeedsMigration(c)
	}
	fmt.Println("Unknown conflict kind. Press enter to continue.")
	_ = readLine()
	return false, nil
}

// printConflictHeader writes the per-conflict identification block
// (kind, project, branch, workspace, details). Pure formatting; no
// state mutation.
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

// resolveNeedsMigration is a one-shot informational stop: tell the user
// to run ws migrate, wait for a key, leave the conflict for the next
// reconciler tick to clear once migration completes.
func resolveNeedsMigration(c conflict.Conflict) (bool, error) {
	fmt.Println("This project needs migration. Run:")
	fmt.Printf("  ws migrate %s\n", c.Project)
	fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
	_ = readLine()
	return false, nil
}

// resolveBranchDuplicate handles two [[branches]] entries with the same
// name in the same project — typically caused by two machines adding
// the same branch concurrently. Offers to open workspace.toml in $EDITOR
// for manual reconciliation; auto-merge is intentionally not offered
// because correctness depends on knowing which CreatedBy/CreatedAt to
// trust, which the tool cannot decide.
func resolveBranchDuplicate(c conflict.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"e", "open workspace.toml in $EDITOR — pick which entry to keep",
			func() (bool, error) { return editWorkspaceForDuplicate(c) }},
	}, "k", "")
}

// editWorkspaceForDuplicate opens workspace.toml in $EDITOR (or vi)
// at the workspace root and asks for confirmation that the duplicate
// is resolved. Returns (true, nil) when the user confirms.
func editWorkspaceForDuplicate(c conflict.Conflict) (bool, error) {
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

// resolveBranchOrphan handles a registered branch whose origin ref has
// disappeared (typical: PR merged with auto-delete-branch on GitHub).
// Two clean exits: drop the entry+worktree (the merged-PR case) or
// keep the local branch (rare; the user wants to preserve unmerged work).
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

// findOrphanWorktree returns the on-disk worktree path for the orphan
// branch on this machine, or "" when this machine never had a local
// checkout of it. Two distinct scenarios converge into "drop":
//   - We have a worktree → user must run `ws worktree rm` themselves
//     to remove it from disk; the registry entry is dropped after.
//   - We never had one (the [[branches]] entry arrived via
//     workspace.toml sync from another machine) → there is nothing
//     on disk to remove; the registry entry is dropped directly.
func findOrphanWorktree(c conflict.Conflict) string {
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

// dropOrphanEntry handles the "d" path. When a local worktree is
// present, it instructs the user to run ws worktree rm and waits for
// confirmation. The registry entry is then removed in both cases so
// the reconciler stops re-recording the orphan.
func dropOrphanEntry(c conflict.Conflict, wtPath string) (bool, error) {
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

// removeBranchFromWorkspace drops the [[branches]] entry for `branch`
// from the named project and persists workspace.toml. No-op if the
// state isn't loaded or the branch isn't registered.
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

// keepOrphanLocal handles the "k" path. Clears last_pushed_* so the
// reconciler's orphan check skips this branch on the next tick — the
// user is intentionally keeping a local-only branch around. A future
// ws worktree push reinstates the field and normal detection resumes.
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
	if err := saveWorkspace(); err != nil {
		fmt.Printf("warning: workspace.toml save failed: %v\n", err)
		return false, nil
	}
	return true, nil
}

// promptAction is one entry on the conflict-resolution menu. The
// handler returns (done, err): done=true exits the loop with that
// resolution status; an error short-circuits the loop unchanged.
type promptAction struct {
	key, label string
	handler    func() (done bool, err error)
}

// runPromptLoop renders `actions` as a numbered menu, reads one line
// of input, dispatches to the matching handler, and repeats until a
// handler returns done=true (or err) or the user picks one of the
// `skipKeys`. Centralizes the "for { print menu; read; switch }" boilerplate
// shared across every conflict-resolution prompt.
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

// dispatchPromptChoice runs the handler whose key matches `choice`.
// Unknown choices are no-ops — the loop re-prompts.
func dispatchPromptChoice(choice string, actions []promptAction) (bool, error) {
	for _, a := range actions {
		if a.key == choice {
			return a.handler()
		}
	}
	return false, nil
}

// resolveTOMLConflict drives the prompt for workspace.toml-level
// conflicts (rebase failed, push rejected). Both options open
// external tooling (shell or git status); we never auto-merge.
func resolveTOMLConflict(c conflict.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"s", "open shell in workspace repo — fix manually, exit shell to return",
			func() (bool, error) { return shellAndConfirm(c.Workspace) }},
		{"d", "show git status",
			func() (bool, error) { return false, runInTerm(c.Workspace, "git", "status") }},
	}, "k", "")
}

// shellAndConfirm spawns a shell rooted at `dir` and asks for
// confirmation that the conflict is resolved when the shell exits.
// Used by both TOML and main-divergence flows.
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

// resolveProjectConflict drives the prompt for project-level
// divergence (main worktree cannot fast-forward). Inspect logs in
// either direction and / or open a shell to fix manually.
func resolveProjectConflict(c conflict.Conflict) (bool, error) {
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

// openShellAtWorktree is the project-conflict "open shell" handler.
// Prints a clear message and stays in the menu when no worktree is
// known; otherwise spawns the shell + confirmation prompt.
func openShellAtWorktree(wtPath string) (bool, error) {
	if wtPath == "" {
		fmt.Println("no worktree path; cannot open shell")
		return false, nil
	}
	return shellAndConfirm(wtPath)
}

// runDivergenceLog runs `git log --oneline <range>` in `wtPath`,
// silently no-op when wtPath is empty. Errors surface to stderr via
// runInTerm rather than as a return value because the caller treats
// the inspection as best-effort.
func runDivergenceLog(wtPath, gitRange string) {
	if wtPath == "" {
		return
	}
	_ = runInTerm(wtPath, "git", "log", "--oneline", gitRange)
}

func findWorktreePath(workspace, project, branch string) (string, error) {
	// Best-effort: ws is loaded, look the project up.
	if ws == nil {
		return "", fmt.Errorf("workspace not loaded")
	}
	proj, ok := ws.Projects[project]
	if !ok {
		return "", fmt.Errorf("project %s not in workspace.toml", project)
	}
	mainPath := workspace + string(os.PathSeparator) + proj.Path
	// For now, return the main worktree. Branch-specific worktree resolution
	// would require parsing `git worktree list` — overkill for the prompt UI.
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
