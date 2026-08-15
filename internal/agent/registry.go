package agent

import (
	"context"
	"errors"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

var errRegistryUnchanged = errors.New("workspace registry unchanged")

func loadRegistryWorkspace(root string) (*config.Workspace, error) {
	local, err := registry.OpenDefault()
	if err != nil {
		return nil, err
	}
	defer func() { _ = local.Close() }()
	workspace, err := local.LoadByRoot(context.Background(), root)
	if err != nil {
		return nil, err
	}
	return workspace.State, nil
}

func mutateRegistryWorkspace(root string, mutate func(*config.Workspace) error) error {
	local, err := registry.OpenDefault()
	if err != nil {
		return err
	}
	defer func() { _ = local.Close() }()
	_, err = local.Mutate(context.Background(), root, mutate)
	if errors.Is(err, errRegistryUnchanged) {
		return nil
	}
	return err
}

func localWorkspaces() ([]registry.Workspace, error) {
	local, err := registry.OpenDefault()
	if err != nil {
		return nil, err
	}
	defer func() { _ = local.Close() }()
	return local.List(context.Background())
}

func findRegistryWorkspace(path string) (registry.Workspace, error) {
	local, err := registry.OpenDefault()
	if err != nil {
		return registry.Workspace{}, err
	}
	defer func() { _ = local.Close() }()
	return local.Find(context.Background(), path)
}
