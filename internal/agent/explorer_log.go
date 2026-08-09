package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func (m *Model) EnableDebugLog() error {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "ws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create explorer log directory: %w", err)
	}
	path := filepath.Join(dir, "explorer.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open explorer log: %w", err)
	}
	m.debugLogFile = file
	m.debugLogPath = path
	m.debugLog = log.New(file, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	m.debugLog.Printf("explorer started pid=%d", os.Getpid())
	return nil
}

func (m *Model) CloseDebugLog() error {
	if m.debugLogFile == nil {
		return nil
	}
	m.debugLog.Printf("explorer stopped")
	return m.debugLogFile.Close()
}

func (m *Model) logLifecycle(format string, args ...any) {
	if m.debugLog != nil {
		m.debugLog.Printf("lifecycle "+format, args...)
	}
}

func (m *Model) DebugLogPath() string {
	return m.debugLogPath
}
