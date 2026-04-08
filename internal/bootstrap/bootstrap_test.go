package bootstrap_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestScanPlan classifies five projects in five distinct on-disk states
// against a synthetic workspace.toml. Verifies bootstrap's state machine.
func TestScanPlan(t *testing.T) {
	wsRoot := t.TempDir()

	// State 1: present (already cloned, has .bare sibling)
	presentRemote := testutil.InitFakeRemote(t, "present", "main")
	presentMain := filepath.Join(wsRoot, "personal", "present")
	presentBare := presentMain + ".bare"
	testutil.RunGit(t, t.TempDir(), "clone", "--bare", presentRemote, presentBare)
	// worktree add main on default branch (just so the dir exists)
	testutil.RunGit(t, presentBare, "worktree", "add", presentMain, "main")

	// State 2: needs-migrate (plain checkout, no .bare)
	testutil.InitFakePlainCheckout(t, filepath.Join(wsRoot, "personal"), "needsmigrate", []string{"main"})

	// State 3: blocked (non-repo files at the path)
	blockedDir := filepath.Join(wsRoot, "personal", "blocked")
	if err := os.MkdirAll(blockedDir, 0o755); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "trash.txt"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write trash: %v", err)
	}

	// State 4: missing (nothing at all)
	// (no setup needed)

	// State 5: would-be-self — same remote we'll set on the workspace's
	// own git origin. Hard to fake without making this a giant test,
	// so we skip the self assertion here and verify it in
	// TestRemotesEqual style elsewhere if needed.

	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"present": {
				Remote: presentRemote,
				Path:   "personal/present",
				Status: config.StatusActive,
			},
			"needsmigrate": {
				Remote: presentRemote, // any remote, won't be used
				Path:   "personal/needsmigrate",
				Status: config.StatusActive,
			},
			"blocked": {
				Remote: presentRemote,
				Path:   "personal/blocked",
				Status: config.StatusActive,
			},
			"missing": {
				Remote: presentRemote,
				Path:   "personal/missing",
				Status: config.StatusActive,
			},
			"archived": {
				Remote: presentRemote,
				Path:   "personal/archived",
				Status: config.StatusArchived, // should be skipped entirely
			},
		},
	}

	plan := bootstrap.ScanPlan(wsRoot, ws, nil)

	// Archived project must be filtered out before classification.
	for _, it := range plan.Items {
		if it.Name == "archived" {
			t.Errorf("archived project leaked into plan")
		}
	}

	cases := map[string]bootstrap.State{
		"present":      bootstrap.StatePresent,
		"needsmigrate": bootstrap.StateNeedsMigrate,
		"blocked":      bootstrap.StateBlocked,
		"missing":      bootstrap.StateMissing,
	}
	for name, want := range cases {
		var got bootstrap.State
		for _, it := range plan.Items {
			if it.Name == name {
				got = it.State
				break
			}
		}
		if got != want {
			t.Errorf("project %s: state = %s, want %s", name, got, want)
		}
	}
}

// TestScanPlan_OnlyFilter restricts the scan to a single named project.
func TestScanPlan_OnlyFilter(t *testing.T) {
	wsRoot := t.TempDir()
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"a": {Path: "a", Status: config.StatusActive},
			"b": {Path: "b", Status: config.StatusActive},
			"c": {Path: "c", Status: config.StatusActive},
		},
	}
	plan := bootstrap.ScanPlan(wsRoot, ws, []string{"b"})
	if len(plan.Items) != 1 || plan.Items[0].Name != "b" {
		t.Errorf("only-filter scan: got %v, want [b]", plan.Items)
	}
}

