package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestResolveExplicitPathCanonicalizesDirectory(t *testing.T) {
	directory := t.TempDir()
	link := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(config.RunnerConfig{ID: "linux-test", Path: link})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != directory {
		t.Fatalf("resolved path = %q, want %q", got, directory)
	}
}

func TestFindByTargetUsesSymbolicIdentity(t *testing.T) {
	defs := []config.RunnerConfig{
		{ID: "linux-group", Workspace: "shared", Group: "personal"},
		{ID: "linux-worktree", Workspace: "shared", Project: "workspace", Worktree: "feat/runner"},
	}
	got, found := FindByTarget(defs, config.RunnerConfig{Workspace: "shared", Project: "workspace", Worktree: "feat/runner"})
	if !found || got.ID != "linux-worktree" {
		t.Fatalf("FindByTarget = %#v, %v", got, found)
	}
	if _, found := FindByTarget(defs, config.RunnerConfig{Workspace: "shared", Project: "workspace"}); found {
		t.Fatal("main project must not match a feature worktree")
	}
}

func TestInspectKeepsRunningProcessWhenTargetIsMissing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	state := runtimeState{ID: "arch-missing", PID: 42, StartTime: 100, Cwd: "/deleted/worktree"}
	if err := saveState(state); err != nil {
		t.Fatal(err)
	}
	info := inspect(config.RunnerConfig{ID: state.ID, Path: "/missing"}, []processInfo{{PID: state.PID, StartTime: state.StartTime}})
	if info.Status != StatusRunning || info.Path != state.Cwd || info.PID != state.PID {
		t.Fatalf("inspect = %#v", info)
	}
}
