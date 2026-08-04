package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncNotificationWorkspacesIncludesLogicalAndSymlinkOwnerOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logical")
	owner := filepath.Join(t.TempDir(), "registry")
	if err := os.MkdirAll(filepath.Join(owner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(owner, "workspace.toml")
	if err := os.WriteFile(tomlPath, []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tomlPath, filepath.Join(root, "workspace.toml")); err != nil {
		t.Fatal(err)
	}
	identities := syncNotificationWorkspaces(root)
	if !identities[cleanAbsolutePath(root)] {
		t.Fatal("logical workspace missing")
	}
	if !identities[cleanAbsolutePath(owner)] {
		t.Fatal("owning repository missing")
	}
	if identities[cleanAbsolutePath(filepath.Dir(owner))] {
		t.Fatal("unrelated workspace included")
	}
}
