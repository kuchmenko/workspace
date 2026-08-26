//go:build !linux

package runner

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/config"
)

type processInfo struct {
	PID       int
	StartTime uint64
	Cwd       string
}

func discoverAmpProcesses() ([]processInfo, error) {
	return nil, fmt.Errorf("Amp runner management requires Linux: %w", os.ErrInvalid)
}

func startProcess(config.RunnerConfig, string) error {
	return fmt.Errorf("Amp runner management requires Linux: %w", os.ErrInvalid)
}

func stopProcess(runtimeState, bool) error {
	return fmt.Errorf("Amp runner management requires Linux: %w", os.ErrInvalid)
}

func stopExternalProcess(runtimeState, bool) error {
	return fmt.Errorf("Amp runner management requires Linux: %w", os.ErrInvalid)
}
