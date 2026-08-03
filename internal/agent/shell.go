package agent

import (
	"fmt"
	"os"
	"syscall"
)

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
