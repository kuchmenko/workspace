package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
)

func setTestRegistryWorkspace(t *testing.T, root string, workspace *config.Workspace) syncnode.Workspace {
	t.Helper()
	directory := t.TempDir()
	store, err := syncnode.OpenStore(filepath.Join(directory, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := syncnode.OpenOrCreateIdentity(filepath.Join(directory, "identity.key"))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	recovery, err := syncnode.CreateRecoveryKey(filepath.Join(directory, "recovery.key"))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	registered, err := store.Import(context.Background(), "test", root, workspace, identity, recovery)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	wsRoot = registered.Root
	ws = registered.State
	nodeStore = store
	nodeState = registered
	nodeID = identity
	t.Cleanup(func() {
		_ = store.Close()
		wsRoot = ""
		ws = nil
		nodeStore = nil
		nodeState = syncnode.Workspace{}
		nodeID = syncnode.Identity{}
	})
	return registered
}
