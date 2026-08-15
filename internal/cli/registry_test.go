package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

func setTestRegistryWorkspace(t *testing.T, root string, workspace *config.Workspace) registry.Workspace {
	t.Helper()
	directory := t.TempDir()
	store, err := registry.Open(filepath.Join(directory, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	registered, err := store.Create(context.Background(), "test", root, workspace)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	wsRoot = registered.Root
	ws = registered.State
	registryStore = store
	registryState = registered
	t.Cleanup(func() {
		_ = store.Close()
		wsRoot = ""
		ws = nil
		registryStore = nil
		registryState = registry.Workspace{}
	})
	return registered
}
