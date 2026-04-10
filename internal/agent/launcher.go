package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// LaunchClaude replaces the current process with `claude` in the given
// working directory. The TUI exits cleanly before this is called —
// bubbletea restores the terminal, then we exec.
//
// If resumeID is non-empty, passes --resume <id> to claude.
func LaunchClaude(cwd, resumeID, prompt string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	args := []string{"claude"}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	if prompt != "" {
		args = append(args, "-p", prompt)
	}

	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %s: %w", cwd, err)
	}

	return syscall.Exec(bin, args, os.Environ())
}

// LaunchShell replaces the current process with the user's $SHELL in
// the given working directory. Used when the user just wants to cd
// into a project/worktree without launching claude.
func LaunchShell(cwd string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %s: %w", cwd, err)
	}

	return syscall.Exec(shell, []string{shell}, os.Environ())
}
