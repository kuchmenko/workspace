package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestWorkspaceRootsUsesMachineConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	registered := []string{t.TempDir(), t.TempDir()}
	sort.Strings(registered)
	if err := config.SaveMachineConfig(&config.MachineConfig{WorkspaceRoots: registered}); err != nil {
		t.Fatal(err)
	}
	fallback := workspaceFixtureRoot(t)

	got := workspaceRoots(fallback)
	if !reflect.DeepEqual(got, registered) {
		t.Fatalf("workspaceRoots = %v, want registered roots %v", got, registered)
	}
}

func TestWorkspaceRootsFallsBackToCurrentRootWhenRegistryEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fallback := workspaceFixtureRoot(t)

	got := workspaceRoots(filepath.Join(fallback, "nested"))
	if !reflect.DeepEqual(got, []string{fallback}) {
		t.Fatalf("workspaceRoots = %v, want fallback [%s]", got, fallback)
	}
}

func workspaceFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.toml"), []byte("[meta]\nversion = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}
