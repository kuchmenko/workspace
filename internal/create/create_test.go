package create

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

// setupCreateWorkspace mirrors add_test.setupWorkspace: temp wsRoot,
// XDG_STATE_HOME isolated to the test, save closure that mutates the
// in-memory workspace pointer. Together they let us exercise the full
// gh→register→clone pipeline without touching real state files.
func setupCreateWorkspace(t *testing.T) (wsRoot string, ws *config.Workspace, save func(*config.Workspace) error) {
	t.Helper()
	wsRoot = t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	ws = &config.Workspace{Projects: map[string]config.Project{}}
	save = func(w *config.Workspace) error {
		ws = w
		return nil
	}
	return
}

// pipelineRunner returns a fakeGHRunner that:
//   - lists `me` + the orgs you give it
//   - succeeds the repo create with the URL the caller passes
//
// Used by both Run and TUI tests.
func pipelineRunner(t *testing.T, htmlURL string, orgs []string) *fakeGHRunner {
	t.Helper()
	return &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.HasPrefix(joined, "api /user/orgs"):
				return strings.Join(orgs, "\n") + "\n", nil
			case strings.HasPrefix(joined, "api /user"):
				return "me\n", nil
			case strings.HasPrefix(joined, "repo create"):
				return htmlURL + "\n", nil
			}
			return "", errors.New("unexpected args: " + joined)
		},
	}
}

// fakeURLFor returns a closure that maps any (owner, name) to the
// supplied bare-repo path. Used as Options.URLFor so clone resolves a
// real local repo instead of attempting an SSH round-trip.
func fakeURLFor(barePath string) func(owner, name string) string {
	return func(_ string, _ string) string { return barePath }
}

func TestRun_Headless_FullPipeline(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	bare := testutil.InitFakeRemote(t, "foo", "main")

	runner := pipelineRunner(t, "https://github.com/me/foo", nil)

	res, err := Run(context.Background(), Options{
		Owner:      "me",
		Name:       "foo",
		Visibility: VisibilityPrivate,
		Mode:       ModeHeadless,
		WsRoot:     wsRoot,
		Workspace:  ws,
		Save:       save,
		GHRunner:   runner,
		URLFor:     fakeURLFor(bare),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Name != "foo" {
		t.Errorf("Name = %q", res.Name)
	}
	if !res.Cloned {
		t.Errorf("Cloned should be true")
	}
	if _, ok := ws.Projects["foo"]; !ok {
		t.Errorf("workspace.Projects missing 'foo': %#v", ws.Projects)
	}

	barePath := filepath.Join(wsRoot, "personal", "foo.bare")
	if _, err := os.Stat(barePath); err != nil {
		t.Errorf("expected bare at %s: %v", barePath, err)
	}
	worktreePath := filepath.Join(wsRoot, "personal", "foo")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("expected worktree at %s: %v", worktreePath, err)
	}
}

func TestRun_Headless_GhRepoCreateInvokedWithCorrectArgs(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	bare := testutil.InitFakeRemote(t, "bar", "main")

	runner := pipelineRunner(t, "https://github.com/org/bar", []string{"org"})

	_, err := Run(context.Background(), Options{
		Owner:       "org",
		Name:        "bar",
		Visibility:  VisibilityPublic,
		Description: "test",
		Category:    config.CategoryWork,
		Mode:        ModeHeadless,
		WsRoot:      wsRoot,
		Workspace:   ws,
		Save:        save,
		GHRunner:    runner,
		URLFor:      fakeURLFor(bare),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Confirm gh was invoked with public + add-readme + description.
	var createCall []string
	for _, c := range runner.calls {
		if len(c) >= 2 && c[0] == "repo" && c[1] == "create" {
			createCall = c
			break
		}
	}
	if createCall == nil {
		t.Fatalf("no repo-create call recorded: %#v", runner.calls)
	}
	joined := strings.Join(createCall, " ")
	for _, want := range []string{"--public", "--add-readme", "--description test"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in call %q", want, joined)
		}
	}
	// Group should default to owner login when category=work.
	if got := ws.Projects["bar"].Group; got != "org" {
		t.Errorf("group = %q, want org (owner login for work)", got)
	}
}

func TestRun_Headless_RejectsBadName(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	_, err := Run(context.Background(), Options{
		Owner:      "me",
		Name:       ".dotfile",
		Visibility: VisibilityPrivate,
		Mode:       ModeHeadless,
		WsRoot:     wsRoot,
		Workspace:  ws,
		Save:       save,
		GHRunner:   &fakeGHRunner{},
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("want ErrInvalidName, got %v", err)
	}
}

func TestRun_Headless_NoOwnerErrors(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	_, err := Run(context.Background(), Options{
		Name:      "foo",
		Mode:      ModeHeadless,
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      save,
	})
	if !errors.Is(err, ErrNoOwner) {
		t.Errorf("want ErrNoOwner, got %v", err)
	}
}

func TestRun_Headless_NoNameErrors(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	_, err := Run(context.Background(), Options{
		Owner:     "me",
		Mode:      ModeHeadless,
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      save,
	})
	if !errors.Is(err, ErrNoName) {
		t.Errorf("want ErrNoName, got %v", err)
	}
}

func TestRun_Sidecar_AcquiredAndReleased(t *testing.T) {
	wsRoot, ws, save := setupCreateWorkspace(t)
	bare := testutil.InitFakeRemote(t, "soloproj", "main")

	if sc := sidecar.AnyActive(wsRoot); sc != nil {
		t.Fatalf("precondition: no sidecar, got %+v", sc)
	}

	_, err := Run(context.Background(), Options{
		Owner:      "me",
		Name:       "soloproj",
		Visibility: VisibilityPrivate,
		Mode:       ModeHeadless,
		WsRoot:     wsRoot,
		Workspace:  ws,
		Save:       save,
		GHRunner:   pipelineRunner(t, "https://github.com/me/soloproj", nil),
		URLFor:     fakeURLFor(bare),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc := sidecar.AnyActive(wsRoot); sc != nil {
		t.Errorf("expected sidecar released, got %+v", sc)
	}
	path, _ := sidecar.Path(wsRoot, sidecar.KindCreate)
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected sidecar file deleted at %s", path)
	}
}

func TestRun_Sidecar_BlocksConcurrent(t *testing.T) {
	wsRoot, _, _ := setupCreateWorkspace(t)
	sc := sidecar.New(wsRoot, sidecar.KindCreate)
	_ = sc.Set(sidecarPayloadKey, sidecarPayload{Mode: ModeHeadless, Owner: "me", Name: "x"})
	if err := sidecar.Save(sc); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), Options{
		Owner:      "me",
		Name:       "y",
		Visibility: VisibilityPrivate,
		Mode:       ModeHeadless,
		WsRoot:     wsRoot,
		Workspace:  &config.Workspace{Projects: map[string]config.Project{}},
		Save:       func(*config.Workspace) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "is running") {
		t.Errorf("want 'is running' error, got %v", err)
	}
	_ = sidecar.Delete(wsRoot, sidecar.KindCreate)
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"foo", true},
		{"foo-bar", true},
		{"foo_bar", true},
		{"foo.bar", true},
		{"a", true},
		{"a1b2c3", true},
		{"", false},
		{".dotfile", false},
		{"-leading-dash", false},
		{"has space", false},
		{"has/slash", false},
		{strings.Repeat("a", 101), false},
		{strings.Repeat("a", 100), true},
	}
	for _, c := range cases {
		err := validateName(c.name)
		if c.ok && err != nil {
			t.Errorf("%q: want ok, got %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: want error, got nil", c.name)
		}
	}
}

func TestRun_RejectsEmptyWsRoot(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Workspace: &config.Workspace{},
		Owner:     "me", Name: "x", Visibility: VisibilityPrivate, Mode: ModeHeadless,
	})
	if err == nil {
		t.Fatal("want error for empty WsRoot")
	}
}

func TestRun_RejectsNilWorkspace(t *testing.T) {
	_, err := Run(context.Background(), Options{
		WsRoot: t.TempDir(),
		Owner:  "me", Name: "x", Visibility: VisibilityPrivate, Mode: ModeHeadless,
	})
	if err == nil {
		t.Fatal("want error for nil Workspace")
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
