//go:build linux

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

type processInfo struct {
	PID       int
	StartTime uint64
	Cwd       string
}

func discoverAmpProcesses() ([]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []processInfo
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || !ampRunnerCommand(cmdline) {
			continue
		}
		start, err := processStartTime(pid)
		if err != nil {
			continue
		}
		cwd := ""
		if resolved, resolveErr := filepath.EvalSymlinks(filepath.Join("/proc", entry.Name(), "cwd")); resolveErr == nil {
			cwd = filepath.Clean(resolved)
		}
		processes = append(processes, processInfo{PID: pid, StartTime: start, Cwd: cwd})
	}
	return processes, nil
}

func ampRunnerCommand(cmdline []byte) bool {
	parts := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	if len(parts) == 0 || filepath.Base(parts[0]) != "amp" {
		return false
	}
	for _, part := range parts[1:] {
		if part == "--no-tui" {
			return true
		}
	}
	return false
}

func processStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 {
		return 0, errors.New("invalid proc stat")
	}
	fields := strings.Fields(string(data[closeParen+1:]))
	if len(fields) <= 19 {
		return 0, errors.New("invalid proc stat fields")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func startProcess(def config.RunnerConfig, cwd string) error {
	root, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	logPath, err := LogPath(def.ID)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	input, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	args := []string{"--no-tui", "--runner-id", def.ID}
	if def.RemoteControlTerminal {
		args = append(args, "--remote-control-terminal")
	} else {
		args = append(args, "--no-remote-control-terminal")
	}
	cmd := exec.Command("amp", args...)
	cmd.Dir = cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	start, err := processStartTime(pid)
	if err != nil {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		return err
	}
	if err := saveState(runtimeState{ID: def.ID, PID: pid, StartTime: start, Cwd: cwd}); err != nil {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func stopProcess(state runtimeState, force bool) error {
	return signalAndWait(state, -state.PID, force)
}

func stopExternalProcess(state runtimeState, force bool) error {
	return signalAndWait(state, state.PID, force)
}

func signalAndWait(state runtimeState, signalTarget int, force bool) error {
	if !sameProcess(state) {
		return nil
	}
	if err := syscall.Kill(signalTarget, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if waitForExit(state, 10*time.Second) {
		return nil
	}
	if !force {
		return fmt.Errorf("runner did not stop after 10s; retry with force")
	}
	if err := syscall.Kill(signalTarget, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if !waitForExit(state, 2*time.Second) {
		return errors.New("runner process remains after SIGKILL")
	}
	return nil
}

func waitForExit(state runtimeState, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !sameProcess(state) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !sameProcess(state)
}

func sameProcess(state runtimeState) bool {
	start, err := processStartTime(state.PID)
	if err != nil || start != state.StartTime {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(state.PID), "cmdline"))
	return err == nil && ampRunnerCommand(cmdline)
}
