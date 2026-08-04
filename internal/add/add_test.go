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
	t.Helper()
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

	lock, err := sidecar.AcquireLock(wsRoot, sidecar.KindAdd)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	_ = sc.Set(sidecarPayloadKey, sidecarPayload{Mode: ModeHeadless, URLCount: 1})
	if err := sidecar.Save(sc); err != nil {
		t.Fatal(err)
	}

	// Second Run must refuse with a descriptive error.
	_, err = Run(context.Background(), Options{
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

// TUI mode tests (TestRun_ModeTUI_*, TestRun_ModeAuto_NoURLs_*) are
// covered by tui_test.go's state-machine drives — Run() in TUI mode
// launches a tea.Program against the real TTY which is not testable
// here. The headless-side dispatch is exercised by the other tests
// in this file.

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

func TestRegisterSaveFailureRestoresWorkspaceAndRetryConverges(t *testing.T) {
	wsRoot, ws, _ := setupWorkspace(t)
	url := fakeRemote(t, "recoverable")
	saveErr := errors.New("disk full")

	_, err := RegisterContext(context.Background(), Options{
		WsRoot: wsRoot, Workspace: ws, Save: func(*config.Workspace) error { return saveErr },
	}, url)
	if !errors.Is(err, saveErr) {
		t.Fatalf("RegisterContext error = %v, want %v", err, saveErr)
	}
	if _, exists := ws.Projects["recoverable"]; exists {
		t.Fatal("failed registration remained in Workspace.Projects")
	}
	if !strings.Contains(err.Error(), "completed layout remains on disk") {
		t.Fatalf("error does not report retained layout: %v", err)
	}
	for _, path := range []string{
		filepath.Join(wsRoot, "personal", "recoverable.bare"),
		filepath.Join(wsRoot, "personal", "recoverable"),
	} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("retained layout %s: %v", path, statErr)
		}
	}

	fresh := &config.Workspace{Projects: map[string]config.Project{}}
	result, err := RegisterContext(context.Background(), Options{
		WsRoot: wsRoot, Workspace: fresh, Save: func(*config.Workspace) error { return nil },
	}, url)
	if err != nil {
		t.Fatalf("retry RegisterContext: %v", err)
	}
	if !result.Cloned || result.Project.DefaultBranch != "main" {
		t.Fatalf("retry result = %+v", result)
	}
	if _, exists := fresh.Projects["recoverable"]; !exists {
		t.Fatal("retry did not register retained layout")
	}
}

func TestAddSidecarDoesNotPersistOrReportURLCredentials(t *testing.T) {
	wsRoot, _, _ := setupWorkspace(t)
	credential := "user:secret-token"
	url := "https://" + credential + "@example.com/owner/repo.git"
	lock, err := acquireSidecar(wsRoot, ModeHeadless, []string{url})
	if err != nil {
		t.Fatal(err)
	}
	path, err := sidecar.Path(wsRoot, sidecar.KindAdd)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), credential) || strings.Contains(string(content), url) {
		t.Fatalf("sidecar contains credentials: %s", content)
	}

	_, err = acquireSidecar(wsRoot, ModeHeadless, []string{url})
	if err == nil {
		t.Fatal("expected concurrent operation error")
	}
	if strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), url) {
		t.Fatalf("concurrent operation error contains credentials: %v", err)
	}
	releaseSidecar(wsRoot, lock)
}

func TestRunHeadlessDoesNotReturnURLCredentials(t *testing.T) {
	wsRoot, ws, save := setupWorkspace(t)
	credential := "user:secret-token"
	url := "https://" + credential + "@example.com/owner/repo.git"
	ws.Projects["repo"] = config.Project{Path: "personal/repo"}

	skipped, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: ws, Save: save, Mode: ModeHeadless, NoClone: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped.Skipped) != 1 {
		t.Fatalf("skipped = %+v", skipped.Skipped)
	}
	if strings.Contains(skipped.Skipped[0].URL+skipped.Skipped[0].Reason, credential) {
		t.Fatalf("skipped result contains credentials: %+v", skipped.Skipped[0])
	}

	failed, err := Run(context.Background(), Options{
		URLs: []string{url}, WsRoot: wsRoot, Workspace: &config.Workspace{}, Save: save, Mode: ModeHeadless, NoClone: true, Category: "invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Errors) != 1 || strings.Contains(failed.Errors[0].Error(), credential) {
		t.Fatalf("failed result = %+v", failed.Errors)
	}
}
