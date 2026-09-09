package sidecar_test

import (
	"errors"
	"os"
	"sync"
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

	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	if err := sc.Set("proj-a", fakeEntry{Branch: "main", When: "now"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := sc.Set("proj-b", fakeEntry{Branch: "master", When: "earlier"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := sidecar.Load(wsRoot, sidecar.KindAdd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil after Save")
		return
	}
	if loaded.Meta.Kind != sidecar.KindAdd {
		t.Errorf("Kind = %s, want add", loaded.Meta.Kind)
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
	sc, err := sidecar.Load("/tmp/never-existed", sidecar.KindAdd)
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
	if err := sidecar.Delete(wsRoot, sidecar.KindAdd); err != nil {
		t.Errorf("Delete on missing: %v", err)
	}

	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := sidecar.Delete(wsRoot, sidecar.KindAdd); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := sidecar.Delete(wsRoot, sidecar.KindAdd); err != nil {
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

	// Add sidecar with our pid should be found.
	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	sc.Meta.Started = time.Now().UTC()
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := sidecar.AnyActive(wsRoot)
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
			Kind:          sidecar.KindAdd,
		},
	}
	if err := sidecar.Save(sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := sidecar.AnyActive(wsRoot); got != nil {
		t.Errorf("AnyActive returned stale sidecar: %+v", got)
	}
}

func TestAnyActiveRecognizesLegacyKinds(t *testing.T) {
	for _, kind := range []sidecar.Kind{sidecar.KindCreate, sidecar.KindBootstrap, sidecar.KindMigrate} {
		t.Run(string(kind)+" live", func(t *testing.T) {
			withStateDir(t)
			wsRoot := t.TempDir()
			if err := sidecar.Save(sidecar.New(wsRoot, kind)); err != nil {
				t.Fatal(err)
			}
			if got := sidecar.AnyActive(wsRoot); got == nil || got.Meta.Kind != kind {
				t.Fatalf("AnyActive = %#v, want live %s sidecar", got, kind)
			}
		})
		t.Run(string(kind)+" stale", func(t *testing.T) {
			withStateDir(t)
			wsRoot := t.TempDir()
			sc := sidecar.New(wsRoot, kind)
			sc.Meta.PID = 999999999
			if err := sidecar.Save(sc); err != nil {
				t.Fatal(err)
			}
			if got := sidecar.AnyActive(wsRoot); got != nil {
				t.Fatalf("AnyActive returned stale %s sidecar: %#v", kind, got)
			}
		})
	}
}

func TestAcquireLockAllowsOnlyOneConcurrentOwner(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/concurrent-lock-test"
	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lock, err := sidecar.AcquireLock(wsRoot, sidecar.KindAdd)
			results <- err
			if err == nil {
				<-release
				_ = lock.Release()
			}
		}()
	}
	close(start)
	first := <-results
	second := <-results
	close(release)
	wg.Wait()

	succeeded := 0
	locked := 0
	for _, err := range []error{first, second} {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, sidecar.ErrLocked):
			locked++
		default:
			t.Fatalf("AcquireLock returned unexpected error: %v", err)
		}
	}
	if succeeded != 1 || locked != 1 {
		t.Fatalf("acquisitions: succeeded=%d locked=%d", succeeded, locked)
	}
}

func TestReleasedOwnerCannotReleaseReplacementOwner(t *testing.T) {
	withStateDir(t)
	wsRoot := "/tmp/replacement-lock-test"
	old, err := sidecar.AcquireLock(wsRoot, sidecar.KindAdd)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	replacement, err := sidecar.AcquireLock(wsRoot, sidecar.KindAdd)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Release()
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := sidecar.AcquireLock(wsRoot, sidecar.KindAdd)
	if third != nil {
		_ = third.Release()
	}
	if !errors.Is(err, sidecar.ErrLocked) {
		t.Fatalf("AcquireLock after old owner release = %v, want ErrLocked", err)
	}
}
