package add

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/sidecar"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// setupWorkspace creates a throwaway workspace dir and returns it plus a
// cleaned-up, minimal config.Workspace. State sidecar dir is redirected
// via XDG_STATE_HOME so tests cannot collide with real `ws add` state.
func setupWorkspace(t *testing.T) (wsRoot string, ws *config.Workspace, saveFn func(*config.Workspace) error) {
	t.Helper()
	wsRoot = t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	ws = &config.Workspace{
		Projects: map[string]config.Project{},
	}

	// Capture save calls so tests can assert persistence happened.
	saveFn = func(w *config.Workspace) error {
		ws = w
		return nil
	}
	return
}

// fakeRemote returns a seeded bare repo URL we can pass as Options.URLs.
// The helper hooks up a single-commit `main` branch so CloneIntoLayout
// can auto-resolve the default branch.
func fakeRemote(t *testing.T, name string) string {
	return testutil.InitFakeRemote(t, name, "main")
}

func TestRun_Headless_SingleURL_RegistersAndClones(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	url := fakeRemote(t, "acme")

	opts := Options{
		URLs:      []string{url},
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      save,
		Mode:      ModeHeadless,
		Category:  config.CategoryPersonal,
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("want 1 added, got %d (%+v)", len(res.Added), res)
	}
	if _, ok := ws.Projects["acme"]; !ok {
		t.Error("expected workspace.Projects to contain 'acme'")
	}

	// Bare+worktree layout must exist on disk.
	barePath := filepath.Join(wsRoot, "personal", "acme.bare")
	worktreePath := filepath.Join(wsRoot, "personal", "acme")
	if _, err := os.Stat(barePath); err != nil {
		t.Errorf("expected bare at %s: %v", barePath, err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("expected worktree at %s: %v", worktreePath, err)
	}
}

func TestRun_Headless_MultipleURLs_PartialFailure_Continues(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)

	// One good URL, one bogus URL. The loop must process both and
	// report the failure in Errors rather than aborting.
	goodURL := fakeRemote(t, "good")
	badURL := "git@invalid-host-that-will-never-resolve.example:foo/bar.git"

	opts := Options{
		URLs:      []string{goodURL, badURL},
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      save,
		Mode:      ModeHeadless,
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run returned error when it should have continued: %v", err)
	}

	if len(res.Added) != 1 {
		t.Errorf("want 1 added (good), got %d", len(res.Added))
	}
	if len(res.Errors) != 1 {
		t.Errorf("want 1 error (bad), got %d", len(res.Errors))
	}
	if _, ok := ws.Projects["good"]; !ok {
		t.Error("good URL must have been registered")
	}
	if _, ok := ws.Projects["bar"]; ok {
		t.Error("bad URL must not have been registered")
	}
}

func TestRun_Headless_AlreadyRegistered_Skipped(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	url := fakeRemote(t, "existing")

	// Pre-register via a first Run.
	first, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless,
	})
	if err != nil || len(first.Added) != 1 {
		t.Fatalf("first Run: %v (res: %+v)", err, first)
	}

	// Second Run with the same URL must be skipped, not errored.
	second, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(second.Added) != 0 {
		t.Errorf("want 0 added on second run, got %d", len(second.Added))
	}
	if len(second.Skipped) != 1 {
		t.Errorf("want 1 skipped on second run, got %d", len(second.Skipped))
	}
}

func TestRun_Sidecar_AcquiredAndReleased(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	url := fakeRemote(t, "soloproj")

	// AnyActive must return nil before Run.
	if sc := sidecar.AnyActive(wsRoot); sc != nil {
		t.Fatalf("precondition: no sidecar, got %+v", sc)
	}

	// Run.
	_, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless,
	})
	if err != nil {
		t.Fatal(err)
	}

	// And must return nil after — Run's defer released the sidecar.
	if sc := sidecar.AnyActive(wsRoot); sc != nil {
		t.Errorf("expected sidecar released, got %+v", sc)
	}

	// No leftover file on disk.
	path, _ := sidecar.Path(wsRoot, sidecar.KindAdd)
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected sidecar file deleted at %s", path)
	}
}

func TestRun_Sidecar_BlocksConcurrentRun(t *testing.T) {
	wsRoot, _, _ := setupWorkspace(t)

	// Simulate a running `ws add` by saving a sidecar with the current
	// process pid (IsAlive will report true).
	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	_ = sc.Set(sidecarPayloadKey, sidecarPayload{Mode: ModeHeadless, URLs: []string{"g@h:a/b.git"}})
	if err := sidecar.Save(sc); err != nil {
		t.Fatal(err)
	}

	// Second Run must refuse with a descriptive error.
	_, err := Run(context.Background(), Options{
		URLs:      []string{"git@example.com:x/y.git"},
		WsRoot:    wsRoot,
		Workspace: &config.Workspace{Projects: map[string]config.Project{}},
		Save:      func(*config.Workspace) error { return nil },
		Mode:      ModeHeadless,
	})
	if err == nil {
		t.Fatal("expected concurrent-run error")
	}
	if !strings.Contains(err.Error(), "is running") {
		t.Errorf("want 'is running' in error, got %v", err)
	}

	// Leave no leftover for other tests.
	_ = sidecar.Delete(wsRoot, sidecar.KindAdd)
}

func TestRun_Sidecar_StaleIsClearedSilently(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	url := fakeRemote(t, "staletest")

	// Save a sidecar with a pid that is absolutely dead.
	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	sc.Meta.PID = 1 // init; we cannot signal it, but our IsAlive test
	// uses signal 0 which will return EPERM from non-root. Use a guaranteed-dead pid instead:
	sc.Meta.PID = 2147483640 // near MAX_INT — unlikely to ever exist
	_ = sc.Set(sidecarPayloadKey, sidecarPayload{Mode: ModeHeadless})
	if err := sidecar.Save(sc); err != nil {
		t.Fatal(err)
	}

	// Stale sidecar must not block the new run.
	res, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Added) != 1 {
		t.Errorf("want 1 added, got %d", len(res.Added))
	}
}

func TestRun_ModeTUI_NotImplemented(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	_, err := Run(context.Background(), Options{
		WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeTUI,
	})
	if !errors.Is(err, ErrTUINotImplemented) {
		t.Errorf("want ErrTUINotImplemented, got %v", err)
	}
}

func TestRun_ModeEmbedded_NotSupported(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	_, err := Run(context.Background(), Options{
		WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeEmbedded,
	})
	if !errors.Is(err, ErrEmbedNotSupported) {
		t.Errorf("want ErrEmbedNotSupported, got %v", err)
	}
}

func TestRun_ModeHeadless_NoURLs_Errors(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	_, err := Run(context.Background(), Options{
		WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless,
	})
	if !errors.Is(err, ErrNoURLs) {
		t.Errorf("want ErrNoURLs, got %v", err)
	}
}

func TestRun_ModeAuto_NoURLs_FallsThroughToTUIErr(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	_, err := Run(context.Background(), Options{
		WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeAuto,
	})
	if !errors.Is(err, ErrTUINotImplemented) {
		t.Errorf("want ErrTUINotImplemented, got %v", err)
	}
}

func TestRun_EmptyWsRoot_Errors(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Workspace: &config.Workspace{Projects: map[string]config.Project{}},
	})
	if err == nil {
		t.Fatal("expected error for empty WsRoot")
	}
}

func TestRun_NilWorkspace_Errors(t *testing.T) {
	_, err := Run(context.Background(), Options{
		WsRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for nil Workspace")
	}
}

func TestRun_CancelledCtxBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, Options{
		WsRoot:    t.TempDir(),
		Workspace: &config.Workspace{},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}
