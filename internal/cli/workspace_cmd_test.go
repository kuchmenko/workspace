package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestWorkspaceCommandsAddListAndRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	cmd := newWorkspaceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"add", workspace})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != workspace {
		t.Fatalf("workspace add output = %q, want %q", got, workspace)
	}

	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != workspace {
		t.Fatalf("workspace list output = %q, want %q", got, workspace)
	}

	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"rm", workspace})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace rm: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != workspace {
		t.Fatalf("workspace rm output = %q, want %q", got, workspace)
	}
	roots, err := config.ListWorkspaceRoots()
	if err != nil {
		t.Fatalf("ListWorkspaceRoots: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("workspace roots after rm = %v, want empty", roots)
	}
}

func TestWorkspaceAddDefaultsToCurrentDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	cmd := newWorkspaceCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace add: %v", err)
	}
	roots, err := config.ListWorkspaceRoots()
	if err != nil {
		t.Fatalf("ListWorkspaceRoots: %v", err)
	}
	if len(roots) != 1 || roots[0] != cwd {
		t.Fatalf("workspace roots = %v, want [%s]", roots, cwd)
	}
}

func TestRootWorkspaceListDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if _, err := os.Stat(filepath.Join(cwd, "workspace.toml")); !os.IsNotExist(err) {
		t.Fatalf("test cwd unexpectedly contains workspace.toml: %v", err)
	}

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"workspace", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("ws workspace list outside a workspace: %v", err)
	}
}

func TestExplorerDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	explorer, _, err := root.Find([]string{"agent"})
	if err != nil {
		t.Fatalf("find agent command: %v", err)
	}
	if err := root.PersistentPreRunE(explorer, nil); err != nil {
		t.Fatalf("agent pre-run outside a workspace: %v", err)
	}
}
