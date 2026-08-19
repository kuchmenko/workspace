package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestResolveOriginDivergenceChoosesLocalOrShared(t *testing.T) {
	for _, test := range []struct {
		name     string
		useLocal bool
	}{
		{name: "local", useLocal: true},
		{name: "shared", useLocal: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, barePath, local, shared := setupOriginDivergence(t)
			if err := resolveOriginDivergenceTo(conflict.Conflict{Workspace: root, Project: "project"}, test.useLocal); err != nil {
				t.Fatalf("resolveOriginDivergenceTo: %v", err)
			}
			chosen := shared
			if test.useLocal {
				chosen = local
			}
			if got, err := git.ConfiguredRemoteURL(barePath, "origin"); err != nil || got != chosen {
				t.Fatalf("origin = %q, %v; want %q", got, err, chosen)
			}
			store, err := registry.OpenDefault()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			workspace, err := store.LoadByRoot(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if got := workspace.State.Projects["project"].Remote; got != chosen {
				t.Fatalf("registry origin = %q, want %q", got, chosen)
			}
			baselines, err := store.OriginBaselines(context.Background(), workspace.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			if got := baselines["project"]; got != chosen {
				t.Fatalf("baseline = %q, want %q", got, chosen)
			}
		})
	}
}

func TestResolveOriginDivergenceRejectsCredentialedLocalOrigin(t *testing.T) {
	root, barePath, _, shared := setupOriginDivergence(t)
	credentialed := "https://user:secret@example.com/project.git"
	if err := git.SetRemoteURL(barePath, credentialed); err != nil {
		t.Fatal(err)
	}
	if err := resolveOriginDivergenceTo(conflict.Conflict{Workspace: root, Project: "project"}, true); err == nil {
		t.Fatal("credentialed local origin was accepted")
	}
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspace.State.Projects["project"].Remote; got != shared {
		t.Fatalf("registry origin = %q, want unchanged %q", got, shared)
	}
	baselines, err := store.OriginBaselines(context.Background(), workspace.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got := baselines["project"]; got == credentialed {
		t.Fatal("credentialed origin was persisted as baseline")
	}
}

func setupOriginDivergence(t *testing.T) (string, string, string, string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	baseline := testutil.InitFakeRemote(t, "baseline", "main")
	local := testutil.InitFakeRemote(t, "local", "main")
	shared := testutil.InitFakeRemote(t, "shared", "main")
	registerSyncTestWorkspace(t, root, &config.Workspace{
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{},
		Projects: map[string]config.Project{"project": {
			Remote: shared, Path: "personal/project", Status: config.StatusActive,
			Category: config.CategoryPersonal, DefaultBranch: "main",
		}},
	})
	barePath := layout.BarePath(filepath.Join(root, "personal", "project"))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, baseline, barePath)
	if err := git.SetRemoteURL(barePath, local); err != nil {
		t.Fatal(err)
	}
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveOriginBaselines(context.Background(), workspace.WorkspaceID, map[string]string{"project": baseline}); err != nil {
		t.Fatal(err)
	}
	return root, barePath, local, shared
}
