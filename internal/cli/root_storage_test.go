package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

func TestLoadCurrentWorkspaceDoesNotFallBackToTOML(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if err := config.Save(root, &config.Workspace{Meta: config.Meta{Version: 1}, Projects: map[string]config.Project{}}); err != nil {
		t.Fatal(err)
	}
	wsRoot = root
	t.Cleanup(resetCLIWorkspace)
	if err := loadCurrentWorkspace(); err == nil || !strings.Contains(err.Error(), "no SQLite workspace found") {
		t.Fatalf("loadCurrentWorkspace error = %v", err)
	}
}

func TestLoadCurrentWorkspaceUsesWSRoot(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	root := t.TempDir()
	command := newWorkspaceCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"create", root, "--name", "selected"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	resetCLIWorkspace()
	t.Setenv("WS_ROOT", root)
	t.Cleanup(resetCLIWorkspace)
	if err := loadCurrentWorkspace(); err != nil {
		t.Fatal(err)
	}
	if registryState.Name != "selected" || wsRoot != root {
		t.Fatalf("loaded workspace = %#v", registryState)
	}
}

func TestAliasCommandUsesSoleRegistryWorkspaceOutsideItsRoot(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	root := t.TempDir()
	command := newWorkspaceCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"create", root, "--name", "selected"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	resetCLIWorkspace()
	t.Chdir(t.TempDir())
	t.Cleanup(resetCLIWorkspace)
	parent := newAliasCmd()
	aliasCommand, _, err := parent.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareCommand(aliasCommand, nil); err != nil {
		t.Fatal(err)
	}
	if registryState.Name != "selected" || wsRoot != root {
		t.Fatalf("loaded workspace = %#v", registryState)
	}
}

func TestAuthCommandsDoNotRequireWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Cleanup(resetCLIWorkspace)
	for _, name := range []string{"login", "logout", "status"} {
		t.Run(name, func(t *testing.T) {
			root := NewRootCmd()
			command, _, err := root.Find([]string{"auth", name})
			if err != nil {
				t.Fatal(err)
			}
			if err = prepareCommand(command, nil); err != nil {
				t.Fatalf("auth %s requires a workspace: %v", name, err)
			}
		})
	}
}

func resetCLIWorkspace() {
	if registryStore != nil {
		_ = registryStore.Close()
	}
	wsRoot = ""
	ws = nil
	wsLoadErr = nil
	registryStore = nil
	registryState = registry.Workspace{}
}
