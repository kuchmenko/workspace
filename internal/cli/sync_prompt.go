package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/layout"
)

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
	for _, action := range actions {
		fmt.Printf("  (%s) %s\n", action.key, action.label)
	}
	for _, key := range skipKeys {
		if key == "" {
			continue
		}
		fmt.Printf("  (%s) skip — leave for later\n", key)
	}
	fmt.Print("> ")
}

func isSkipKey(choice string, skipKeys []string) bool {
	for _, key := range skipKeys {
		if choice == key {
			return true
		}
	}
	return false
}

func dispatchPromptChoice(choice string, actions []promptAction) (bool, error) {
	for _, action := range actions {
		if action.key == choice {
			return action.handler()
		}
	}
	return false, nil
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
		return "", fmt.Errorf("project %s not in workspace registry", project)
	}
	mainPath := workspace + string(os.PathSeparator) + proj.Path
	if branch == "" {
		return mainPath, nil
	}
	barePath := layout.BarePath(mainPath)
	if wtPath := locateWorktreeForBranch(barePath, branch); wtPath != "" {
		return wtPath, nil
	}
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
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
