package agent

import (
	"context"
	"errors"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
)

var errRegistryUnchanged = errors.New("workspace registry unchanged")

func loadRegistryWorkspace(root string) (*config.Workspace, error) {
	node, err := syncnode.OpenNode()
	if err != nil {
		return nil, err
	}
	defer func() { _ = node.Close() }()
	workspace, err := node.LoadByRoot(context.Background(), root)
	if err != nil {
		return nil, err
	}
	return workspace.State, nil
}

func mutateRegistryWorkspace(root string, mutate func(*config.Workspace) error) error {
	node, err := syncnode.OpenNode()
	if err != nil {
		return err
	}
	defer func() { _ = node.Close() }()
	_, err = node.Mutate(context.Background(), root, mutate)
	if errors.Is(err, errRegistryUnchanged) {
		return nil
	}
	return err
}

func nodeWorkspaces() ([]syncnode.Workspace, error) {
	node, err := syncnode.OpenNode()
	if err != nil {
		return nil, err
	}
	defer func() { _ = node.Close() }()
	return node.List(context.Background())
}

func findRegistryWorkspace(path string) (syncnode.Workspace, error) {
	node, err := syncnode.OpenNode()
	if err != nil {
		return syncnode.Workspace{}, err
	}
	defer func() { _ = node.Close() }()
	return node.Find(context.Background(), path)
}
