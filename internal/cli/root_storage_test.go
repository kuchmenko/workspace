package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
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
	command.SetArgs([]string{"create", root, "--name", "selected", "--recovery-key", filepath.Join(directory, "recovery.key")})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	resetCLIWorkspace()
	t.Setenv("WS_ROOT", root)
	t.Cleanup(resetCLIWorkspace)
	if err := loadCurrentWorkspace(); err != nil {
		t.Fatal(err)
	}
	if nodeState.Name != "selected" || wsRoot != root {
		t.Fatalf("loaded workspace = %#v", nodeState)
	}
}

func resetCLIWorkspace() {
	if nodeStore != nil {
		_ = nodeStore.Close()
	}
	wsRoot = ""
	ws = nil
	wsLoadErr = nil
	nodeStore = nil
	nodeState = syncnode.Workspace{}
	nodeID = syncnode.Identity{}
}
