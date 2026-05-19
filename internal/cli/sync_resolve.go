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

func openConflictStore() (*conflict.Store, error) {
	return conflict.Open()
}

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

func resolveBranchDuplicate(c conflict.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"e", "open workspace.toml in $EDITOR — pick which entry to keep",
			func() (bool, error) { return editWorkspaceForDuplicate(c) }},
	}, "k", "")
}

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

func resolveTOMLConflict(c conflict.Conflict) (bool, error) {
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
