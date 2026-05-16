package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestStampLaunchFromPath_BumpsActivityOnMainBranch confirms that a
// launch into the main worktree, where the default branch has never
// been registered in [[branches]], creates a minimal branch entry and
// stamps last_active_at. This is the common case: the user opens a
// claude session on `main` and expects the project to show up in
// Recent on the next ws agent invocation.
func TestStampLaunchFromPath_BumpsActivityOnMainBranch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	wsRoot := t.TempDir()
	// Plain checkout serves as the project's main worktree. The
	// stamper only reads HEAD with `git rev-parse`; it does not care
	// about the bare/worktree layout. Path is registered as just
	// "alpha" so cwd == wsRoot/alpha.
	mainPath := testutil.InitFakePlainCheckout(t, wsRoot, "alpha", []string{"main"})

	seedWorkspace(t, wsRoot, map[string]config.Project{
		"alpha": {
			Remote:        "git@github.com:user/alpha.git",
			Path:          "alpha",
			Status:        config.StatusActive,
			Category:      config.CategoryPersonal,
			DefaultBranch: "main",
		},
	})

	seedMachine(t, "linux")

	if err := StampLaunchFromPath(mainPath); err != nil {
		t.Fatalf("StampLaunchFromPath: %v", err)
	}

	got, err := config.Load(wsRoot)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	alpha := got.Projects["alpha"]
	if len(alpha.Branches) != 1 {
		t.Fatalf("expected 1 branch entry after stamp, got %d: %+v", len(alpha.Branches), alpha.Branches)
	}
	b := alpha.Branches[0]
	if b.Name != "main" {
		t.Errorf("stamped wrong branch: got %q, want %q", b.Name, "main")
	}
	if b.LastActiveMachine != "linux" {
		t.Errorf("LastActiveMachine: got %q, want %q", b.LastActiveMachine, "linux")
	}
	if b.LastActiveAt == "" {
		t.Error("LastActiveAt should be non-empty after stamp")
	}
	// Auto-created entries must leave CreatedBy/CreatedAt empty —
	// the launcher is NOT a creation event, unlike `ws worktree add`.
	if b.CreatedBy != "" || b.CreatedAt != "" {
		t.Errorf("auto-created stamp must not set CreatedBy/CreatedAt: got %+v", b)
	}
}

// TestStampLaunchFromPath_OutsideWorkspace_NoOp confirms the stamper
// is silent when the path does not belong to any workspace project.
// This is a hot path — every `ws agent shell <random-path>` invocation
// must not error out.
func TestStampLaunchFromPath_OutsideWorkspace_NoOp(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	other := t.TempDir()
	if err := StampLaunchFromPath(other); err != nil {
		t.Errorf("stamping outside any workspace must not error, got %v", err)
	}
}

// TestStampLaunchFromPath_UpdatesExistingBranch confirms that a
// second launch on the same branch bumps the timestamp in-place
// without producing duplicate [[branches]] entries.
func TestStampLaunchFromPath_UpdatesExistingBranch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	wsRoot := t.TempDir()
	mainPath := testutil.InitFakePlainCheckout(t, wsRoot, "alpha", []string{"main"})

	seedWorkspace(t, wsRoot, map[string]config.Project{
		"alpha": {
			Path: "alpha", Status: config.StatusActive,
			Category: config.CategoryPersonal, DefaultBranch: "main",
			Branches: []config.BranchMeta{{
				Name:              "main",
				Machines:          []string{"linux"},
				LastActiveMachine: "linux",
				LastActiveAt:      "2026-04-01T00:00:00Z",
				CreatedBy:         "linux",
				CreatedAt:         "2026-04-01T00:00:00Z",
			}},
		},
	})
	seedMachine(t, "linux")

	if err := StampLaunchFromPath(mainPath); err != nil {
		t.Fatalf("StampLaunchFromPath: %v", err)
	}

	got, _ := config.Load(wsRoot)
	alpha := got.Projects["alpha"]
	if len(alpha.Branches) != 1 {
		t.Fatalf("expected branch count unchanged, got %d: %+v", len(alpha.Branches), alpha.Branches)
	}
	b := alpha.Branches[0]
	if b.LastActiveAt == "2026-04-01T00:00:00Z" {
		t.Error("LastActiveAt should have been bumped past the seeded 2026-04-01")
	}
	if b.CreatedAt != "2026-04-01T00:00:00Z" {
		t.Errorf("CreatedAt must not be modified by stamp: got %q", b.CreatedAt)
	}
}

// TestStampLaunchFromPath_FindRootFrom_HandlesSubpath confirms the
// stamper walks up from a sub-directory of the project to locate
// workspace.toml — important because the user often launches from
// some path beneath the worktree root.
func TestStampLaunchFromPath_FindRootFrom_HandlesSubpath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	wsRoot := t.TempDir()
	mainPath := testutil.InitFakePlainCheckout(t, wsRoot, "alpha", []string{"main"})

	seedWorkspace(t, wsRoot, map[string]config.Project{
		"alpha": {
			Path: "alpha", Status: config.StatusActive,
			Category: config.CategoryPersonal, DefaultBranch: "main",
		},
	})
	seedMachine(t, "linux")

	// Launch from a real subdir inside the worktree to exercise the
	// walk-up logic in FindRootFrom — using mainPath itself or "." on
	// it would skip the walk entirely.
	deep := filepath.Join(mainPath, "src", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	if err := StampLaunchFromPath(deep); err != nil {
		t.Fatalf("StampLaunchFromPath: %v", err)
	}
	got, _ := config.Load(wsRoot)
	if len(got.Projects["alpha"].Branches) != 1 {
		t.Errorf("expected branch entry, got %+v", got.Projects["alpha"].Branches)
	}
}

// seedWorkspace writes a minimal workspace.toml at root with the given
// projects map. Other fields are left at zero/defaults; tests assert
// only on what they set.
func seedWorkspace(t *testing.T, root string, projects map[string]config.Project) {
	t.Helper()
	ws := &config.Workspace{
		Meta:     config.Meta{Version: 1, Root: root},
		Projects: projects,
	}
	if err := config.Save(root, ws); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
}

// seedMachine writes ~/.config/ws/config.toml under the test's
// XDG_CONFIG_HOME so loadMachineName returns deterministically.
func seedMachine(t *testing.T, name string) {
	t.Helper()
	cfg := &config.MachineConfig{MachineName: name}
	if err := config.SaveMachineConfig(cfg); err != nil {
		t.Fatalf("seed machine: %v", err)
	}
}
