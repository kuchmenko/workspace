package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestLoadWorkspacesUsesSQLiteRegistry(t *testing.T) {
	first := workspaceFixtureRoot(t)
	second := workspaceFixtureRoot(t)
	workspaces, diagnostics := LoadWorkspaces("")
	if len(diagnostics) != 0 || len(workspaces) != 2 {
		t.Fatalf("workspaces = %#v, diagnostics = %#v", workspaces, diagnostics)
	}
	roots := map[string]bool{workspaces[0].Root: true, workspaces[1].Root: true}
	if !roots[first] || !roots[second] {
		t.Fatalf("loaded roots = %#v", roots)
	}
}

func TestLoadWorkspacesReportsEmptyRegistry(t *testing.T) {
	isolateRegistryFixture(t)
	workspaces, diagnostics := LoadWorkspaces("")
	if len(workspaces) != 0 || len(diagnostics) != 1 {
		t.Fatalf("workspaces = %#v, diagnostics = %#v", workspaces, diagnostics)
	}
}

func workspaceFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	saveRegistryFixture(t, root, &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}})
	return root
}
