package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kuchmenko/workspace/internal/conflict"
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

	switch c.Kind {
	case conflict.KindTOMLMerge, conflict.KindTOMLPushFailed:
		return resolveTOMLConflict(c)
	case conflict.KindBranchDivergence, conflict.KindMainDivergence:
		return resolveProjectConflict(c)
	case conflict.KindNeedsMigration:
		fmt.Println("This project needs migration. Run:")
		fmt.Printf("  ws migrate %s\n", c.Project)
		fmt.Println("Press enter to continue (the conflict will clear automatically on next sync).")
		_ = readLine()
		return false, nil
	default:
		fmt.Println("Unknown conflict kind. Press enter to continue.")
		_ = readLine()
		return false, nil
	}
}

func resolveTOMLConflict(c conflict.Conflict) (bool, error) {
	for {
		fmt.Println("Options:")
		fmt.Println("  (s) open shell in workspace repo — fix manually, exit shell to return")
		fmt.Println("  (d) show git status")
		fmt.Println("  (k) skip — leave for later")
		fmt.Print("> ")
		choice := strings.TrimSpace(readLine())
		switch choice {
		case "s":
			if err := openShell(c.Workspace); err != nil {
				return false, err
			}
			fmt.Println("returned from shell. Mark conflict resolved? (y/N)")
			if strings.EqualFold(strings.TrimSpace(readLine()), "y") {
				return true, nil
			}
		case "d":
			if err := runInTerm(c.Workspace, "git", "status"); err != nil {
				return false, err
			}
		case "k", "":
			return false, nil
		}
	}
}

func resolveProjectConflict(c conflict.Conflict) (bool, error) {
	wtPath, err := findWorktreePath(c.Workspace, c.Project, c.Branch)
	if err != nil {
		fmt.Printf("warning: could not locate worktree: %v\n", err)
	}
	for {
		fmt.Println("Options:")
		fmt.Println("  (l) git log local..remote (remote-only commits)")
		fmt.Println("  (r) git log remote..local (local-only commits)")
		fmt.Println("  (o) open shell in worktree — fix manually")
		fmt.Println("  (k) skip — leave for later")
		fmt.Print("> ")
		choice := strings.TrimSpace(readLine())
		switch choice {
		case "l":
			if wtPath != "" {
				_ = runInTerm(wtPath, "git", "log", "--oneline", c.Branch+"..@{u}")
			}
		case "r":
			if wtPath != "" {
				_ = runInTerm(wtPath, "git", "log", "--oneline", "@{u}.."+c.Branch)
			}
		case "o":
			if wtPath == "" {
				fmt.Println("no worktree path; cannot open shell")
				continue
			}
			if err := openShell(wtPath); err != nil {
				return false, err
			}
			fmt.Println("returned from shell. Mark conflict resolved? (y/N)")
			if strings.EqualFold(strings.TrimSpace(readLine()), "y") {
				return true, nil
			}
		case "k", "":
			return false, nil
		}
	}
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
