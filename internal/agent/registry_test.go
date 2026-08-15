package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
)

func saveRegistryFixture(t *testing.T, root string, workspace *config.Workspace) {
	t.Helper()
	isolateRegistryFixture(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	node, err := syncnode.OpenNode()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = node.Close() }()
	loaded, err := node.LoadByRoot(context.Background(), root)
	if errors.Is(err, syncnode.ErrWorkspaceNotFound) {
		recovery, createErr := syncnode.CreateRecoveryKey(filepath.Join(t.TempDir(), "recovery.key"))
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err = node.Import(context.Background(), filepath.Base(root), root, workspace, recovery); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Mutate(context.Background(), root, func(current *config.Workspace) error {
		*current = *workspace
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = loaded
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
