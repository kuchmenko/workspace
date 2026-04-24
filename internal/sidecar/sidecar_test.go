package sidecar_test

import (
	"os"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

// withStateDir points $XDG_STATE_HOME at a temp dir for the duration of
// the test, so we never touch the user's real state dir.
func withStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

type fakeEntry struct {
	Branch string `json:"branch"`
	When   string `json:"when"`
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/fake-workspace"

	sc := sidecar.New(wsRoot, sidecar.KindBootstrap)
	if err := sc.Set("proj-a", fakeEntry{Branch: "main", When: "now"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := sc.Set("proj-b", fakeEntry{Branch: "master", When: "earlier"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := sidecar.Load(wsRoot, sidecar.KindBootstrap)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil after Save")
	}
	if loaded.Meta.Kind != sidecar.KindBootstrap {
		t.Errorf("Kind = %s, want bootstrap", loaded.Meta.Kind)
	}
	if loaded.Meta.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", loaded.Meta.PID, os.Getpid())
	}
	if !loaded.Has("proj-a") || !loaded.Has("proj-b") {
		t.Errorf("missing entries after round-trip: %v", loaded.Done)
	}
	var got fakeEntry
	if _, err := loaded.Get("proj-a", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Branch != "main" {
		t.Errorf("Get branch = %s, want main", got.Branch)
	}
}

func TestLoadMissingReturnsNil(t *testing.T) {
	withStateDir(t)
	sc, err := sidecar.Load("/tmp/never-existed", sidecar.KindMigrate)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sc != nil {
		t.Errorf("Load of missing sidecar returned non-nil: %+v", sc)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/fake-workspace"
	if err := sidecar.Delete(wsRoot, sidecar.KindBootstrap); err != nil {
		t.Errorf("Delete on missing: %v", err)
	}

	sc := sidecar.New(wsRoot, sidecar.KindBootstrap)
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := sidecar.Delete(wsRoot, sidecar.KindBootstrap); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := sidecar.Delete(wsRoot, sidecar.KindBootstrap); err != nil {
		t.Errorf("Delete second time: %v", err)
	}
}

func TestIsAliveSelfPID(t *testing.T) {
	sc := &sidecar.Sidecar{
		Meta: sidecar.Meta{PID: os.Getpid()},
	}
	if !sidecar.IsAlive(sc) {
		t.Errorf("IsAlive should return true for our own pid")
	}
}

func TestIsAliveDeadPID(t *testing.T) {
	// pid 0 is invalid; pid 1 (init) is always alive on Linux but might
	// fail with EPERM. Use a deliberately bogus high pid that should not
	// exist on any normal system.
	sc := &sidecar.Sidecar{
		Meta: sidecar.Meta{PID: 999999999},
	}
	if sidecar.IsAlive(sc) {
		t.Errorf("IsAlive returned true for bogus pid")
	}
}

func TestIsAliveZeroPID(t *testing.T) {
	sc := &sidecar.Sidecar{Meta: sidecar.Meta{PID: 0}}
	if sidecar.IsAlive(sc) {
		t.Errorf("IsAlive returned true for pid 0")
	}
	if sidecar.IsAlive(nil) {
		t.Errorf("IsAlive returned true for nil sidecar")
	}
}

func TestAnyActiveFindsBoth(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/anyactive-test"

	// No sidecars yet → nil.
	if got := sidecar.AnyActive(wsRoot); got != nil {
		t.Errorf("AnyActive returned %+v, want nil", got)
	}

	// Bootstrap sidecar with our pid → should be found.
	sc := sidecar.New(wsRoot, sidecar.KindBootstrap)
	sc.Meta.Started = time.Now().UTC()
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := sidecar.AnyActive(wsRoot)
	if got == nil || got.Meta.Kind != sidecar.KindBootstrap {
		t.Errorf("AnyActive after bootstrap save: %+v", got)
	}

	// Cleanup
	_ = sidecar.Delete(wsRoot, sidecar.KindBootstrap)

	// Migrate sidecar instead.
	sc2 := sidecar.New(wsRoot, sidecar.KindMigrate)
	if err := sidecar.Save(sc2); err != nil {
		t.Fatalf("Save migrate: %v", err)
	}
	got = sidecar.AnyActive(wsRoot)
	if got == nil || got.Meta.Kind != sidecar.KindMigrate {
		t.Errorf("AnyActive after migrate save: %+v", got)
	}

	// Cleanup migrate before testing add (AnyActive returns the first
	// active sidecar in its iteration order, which is Bootstrap →
	// Migrate → Add; leftover migrate would mask the add result).
	_ = sidecar.Delete(wsRoot, sidecar.KindMigrate)

	// Add sidecar: Track B Phase 1-C — the reconciler must pause for
	// `ws add` runs the same way it does for bootstrap and migrate.
	sc3 := sidecar.New(wsRoot, sidecar.KindAdd)
	if err := sidecar.Save(sc3); err != nil {
		t.Fatalf("Save add: %v", err)
	}
	got = sidecar.AnyActive(wsRoot)
	if got == nil || got.Meta.Kind != sidecar.KindAdd {
		t.Errorf("AnyActive after add save: %+v", got)
	}
	_ = sidecar.Delete(wsRoot, sidecar.KindAdd)
}

func TestAnyActiveIgnoresStale(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/stale-test"

	// Hand-craft a sidecar with a bogus pid → stale.
	sc := &sidecar.Sidecar{
		Meta: sidecar.Meta{
			PID:           999999999,
			Started:       time.Now().UTC().Add(-1 * time.Hour),
			WorkspaceRoot: wsRoot,
			Kind:          sidecar.KindBootstrap,
		},
	}
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := sidecar.AnyActive(wsRoot); got != nil {
		t.Errorf("AnyActive returned stale sidecar: %+v", got)
	}
}
