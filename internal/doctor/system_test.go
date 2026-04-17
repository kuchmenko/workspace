package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

// isolateState redirects $XDG_STATE_HOME and $XDG_CONFIG_HOME to temp
// dirs so tests cannot read or write the real user config / state.
// Applies t.Cleanup to restore the original env afterwards.
func isolateState(t *testing.T) (stateDir, configDir string) {
	t.Helper()
	stateDir = t.TempDir()
	configDir = t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("XDG_CONFIG_HOME", configDir)
	// daemon.PidPath goes via os.UserConfigDir which on Linux honors
	// XDG_CONFIG_HOME. Force HOME too so macOS / other platforms that
	// compute dirs from HOME also land in the tempdir.
	t.Setenv("HOME", t.TempDir())
	return
}

func TestCheckDaemon_NotRunning(t *testing.T) {
	isolateState(t)
	f := checkDaemon()
	if f.Severity != Warn {
		t.Fatalf("Severity=%s want Warn (no PID file)", f.Severity)
	}
	if f.FixHint == "" {
		t.Fatalf("FixHint should suggest `ws daemon start`")
	}
}

func TestCheckStaleSidecars_None(t *testing.T) {
	isolateState(t)
	f := checkStaleSidecars(t.TempDir())
	if f.Severity != OK {
		t.Fatalf("Severity=%s want OK when no sidecars exist", f.Severity)
	}
}

func TestCheckStaleSidecars_DeadPidFixed(t *testing.T) {
	isolateState(t)
	wsRoot := t.TempDir()

	sc := sidecar.New(wsRoot, sidecar.KindBootstrap)
	// PID 1 on Linux is init, always alive. Pick a pid that cannot exist
	// on this host: the max allowed pid plus one. Any large value that
	// isn't actively in use works, but to avoid flakes we use a PID we
	// know is unused by sampling the current table and picking one well
	// beyond the max.
	sc.Meta.PID = 0x7fffffff // INT32_MAX — no process can have this pid
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("sidecar.Save: %v", err)
	}

	f := checkStaleSidecars(wsRoot)
	if f.Severity != Warn {
		t.Fatalf("Severity=%s want Warn for stale sidecar", f.Severity)
	}
	if f.Fix == nil {
		t.Fatal("stale sidecar finding should carry an auto-fix")
	}
	if err := f.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	// Re-run: file should be gone, check should be OK.
	again := checkStaleSidecars(wsRoot)
	if again.Severity != OK {
		t.Fatalf("after fix: Severity=%s want OK", again.Severity)
	}
}

func TestCheckConflicts_None(t *testing.T) {
	isolateState(t)
	f := checkConflicts(t.TempDir())
	if f.Severity != OK {
		t.Fatalf("Severity=%s want OK", f.Severity)
	}
}

func TestCheckConflicts_Mine(t *testing.T) {
	isolateState(t)
	wsRoot := t.TempDir()

	store, err := conflict.Open()
	if err != nil {
		t.Fatalf("conflict.Open: %v", err)
	}
	absWsRoot, _ := filepath.Abs(wsRoot)
	if _, err := store.Record(conflict.Conflict{
		Workspace: absWsRoot,
		Project:   "demo",
		Branch:    "wt/test/foo",
		Kind:      conflict.KindBranchDivergence,
		Details:   json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	f := checkConflicts(wsRoot)
	if f.Severity != Error {
		t.Fatalf("Severity=%s want Error", f.Severity)
	}
	if f.FixHint == "" {
		t.Fatal("conflicts finding must point at `ws sync resolve`")
	}
}

func TestCheckConflicts_OtherWorkspaceIgnored(t *testing.T) {
	isolateState(t)
	wsRoot := t.TempDir()
	other := t.TempDir()

	store, err := conflict.Open()
	if err != nil {
		t.Fatalf("conflict.Open: %v", err)
	}
	absOther, _ := filepath.Abs(other)
	if _, err := store.Record(conflict.Conflict{
		Workspace: absOther,
		Kind:      conflict.KindTOMLMerge,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	f := checkConflicts(wsRoot)
	if f.Severity != OK {
		t.Fatalf("conflict belonging to another workspace should be ignored; got %s", f.Severity)
	}
}

func TestCheckConfig_Valid(t *testing.T) {
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"demo": {
				Remote:   "git@github.com:example/demo.git",
				Path:     "personal/demo",
				Status:   config.StatusActive,
				Category: config.CategoryPersonal,
			},
		},
		Daemon: config.Daemon{
			PollInterval:   "5m",
			StaleThreshold: "30d",
		},
	}
	f := checkConfig(ws)
	if f.Severity != OK {
		t.Fatalf("Severity=%s want OK (%s)", f.Severity, f.Message)
	}
}

func TestCheckConfig_Invalid(t *testing.T) {
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"broken": {
				Remote: "", // missing
				Path:   "", // missing
				Status: "weird",
			},
		},
		Daemon: config.Daemon{
			PollInterval: "not-a-duration",
		},
	}
	f := checkConfig(ws)
	if f.Severity != Error {
		t.Fatalf("Severity=%s want Error", f.Severity)
	}
	// All three issues (remote, path, status) + one daemon issue should
	// surface, but we don't want to pin on exact count — just >= 3.
	// Smoke-check one phrase per class.
	for _, needle := range []string{"missing remote", "missing path", "unknown status", "not a valid duration"} {
		if !contains(f.Message, needle) {
			t.Errorf("Message missing %q: %s", needle, f.Message)
		}
	}
}

func TestValidDuration(t *testing.T) {
	cases := map[string]bool{
		"5m":       true,
		"1h30m":    true,
		"30d":      true,
		"":         false,
		"d":        false,
		"garbage":  false,
		"5minutes": false, // time.ParseDuration doesn't accept this
	}
	for in, want := range cases {
		if got := validDuration(in); got != want {
			t.Errorf("validDuration(%q)=%v want %v", in, got, want)
		}
	}
}

// Round-trip sidecar through toml to make sure our test fixture (a
// minimal New + PID override) agrees with the on-disk schema. Guards
// against silent breakage if the sidecar format changes.
func TestSidecarFixtureRoundtrip(t *testing.T) {
	isolateState(t)
	wsRoot := t.TempDir()
	sc := sidecar.New(wsRoot, sidecar.KindBootstrap)
	sc.Meta.PID = 0x7fffffff
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, err := sidecar.Path(wsRoot, sidecar.KindBootstrap)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var back sidecar.Sidecar
	if err := toml.Unmarshal(data, &back); err != nil {
		t.Fatalf("toml.Unmarshal: %v", err)
	}
	if back.Meta.PID != sc.Meta.PID {
		t.Fatalf("PID round-trip: got %d want %d", back.Meta.PID, sc.Meta.PID)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
