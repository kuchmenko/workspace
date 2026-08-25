//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestMain(m *testing.M) {
	if slices.Contains(os.Args, "--no-tui") {
		select {}
	}
	os.Exit(m.Run())
}

func TestAmpRunnerCommand(t *testing.T) {
	if !ampRunnerCommand([]byte("/home/user/.local/bin/amp\x00--no-tui\x00--runner-id\x00linux-test\x00")) {
		t.Fatal("Amp runner command was not recognized")
	}
	for _, command := range [][]byte{
		[]byte("/home/user/.local/bin/amp\x00--execute\x00hello\x00"),
		[]byte("/usr/bin/bash\x00-c\x00amp --no-tui\x00"),
	} {
		if ampRunnerCommand(command) {
			t.Fatalf("non-runner command was recognized: %q", command)
		}
	}
}

func TestProcessStartTimeReadsCurrentProcess(t *testing.T) {
	start, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("processStartTime: %v", err)
	}
	if start == 0 {
		t.Fatal("process start time is zero")
	}
}

func TestRunnerLifecycleWithDetachedProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(bin, "amp")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	def := config.RunnerConfig{ID: "test-detached", Path: t.TempDir()}
	info, err := Start(def)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.Status != StatusRunning || info.PID <= 0 {
		t.Fatalf("started runner = %#v", info)
	}
	if err := Shutdown(def, false); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if info := Inspect(def); info.Status != StatusStopped {
		t.Fatalf("runner after shutdown = %#v", info)
	}
}

func TestShutdownExternalSignalsExactProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(bin, "amp")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	def := config.RunnerConfig{ID: "test-external", Path: t.TempDir()}
	if err := startProcess(def, def.Path); err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	state, err := loadState(def.ID)
	if err != nil {
		t.Fatal(err)
	}
	info := Info{Status: StatusOccupied, Path: def.Path, PID: state.PID, StartTime: state.StartTime}
	if err := ShutdownExternal(info, false); err != nil {
		t.Fatalf("ShutdownExternal: %v", err)
	}
	if sameProcess(state) {
		t.Fatal("external process is still running")
	}
}
