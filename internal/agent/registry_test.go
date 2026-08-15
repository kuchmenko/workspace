package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

func saveRegistryFixture(t *testing.T, root string, workspace *config.Workspace) {
	t.Helper()
	isolateRegistryFixture(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	local, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	_, err = local.LoadByRoot(context.Background(), root)
	if errors.Is(err, registry.ErrWorkspaceNotFound) {
		if _, err = local.Create(context.Background(), filepath.Base(root), root, workspace); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = local.Mutate(context.Background(), root, func(current *config.Workspace) error {
		*current = *workspace
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func loadRegistryFixture(t *testing.T, root string) *config.Workspace {
	t.Helper()
	workspace, err := loadRegistryWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func isolateRegistryFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("WS_AGENT_TEST_STATE") != "" {
		return
	}
	directory := t.TempDir()
	t.Setenv("WS_AGENT_TEST_STATE", directory)
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
}
