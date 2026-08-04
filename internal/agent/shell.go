package agent

import (
	"fmt"
	"os"
	"syscall"

	"github.com/kuchmenko/workspace/internal/metrics"
)

func LaunchShell(cwd string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %s: %w", cwd, err)
	}

	metrics.RecordExplorerShellOpened()
	return syscall.Exec(shell, []string{shell}, os.Environ())
}
