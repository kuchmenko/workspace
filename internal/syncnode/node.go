package syncnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

type Node struct {
	store    *Store
	identity Identity
}

func OpenNode() (*Node, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(paths.Database)
	if err != nil {
		return nil, err
	}
	identity, err := OpenOrCreateIdentity(paths.Identity)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &Node{store: store, identity: identity}, nil
}

func (node *Node) Close() error {
	return node.store.Close()
}

func (node *Node) List(ctx context.Context) ([]Workspace, error) {
	return node.store.List(ctx)
}

func (node *Node) LoadByName(ctx context.Context, name string) (Workspace, error) {
	return node.store.LoadByName(ctx, name)
}

func (node *Node) LoadByRoot(ctx context.Context, root string) (Workspace, error) {
	return node.store.LoadByRoot(ctx, root)
}

func (node *Node) Import(ctx context.Context, name, root string, workspace *config.Workspace, recoveryPublicKey []byte) (Workspace, error) {
	return node.store.Import(ctx, name, root, workspace, node.identity, recoveryPublicKey)
}

func (node *Node) Find(ctx context.Context, path string) (Workspace, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	workspaces, err := node.List(ctx)
	if err != nil {
		return Workspace{}, err
	}
	var found Workspace
	for _, candidate := range workspaces {
		relative, relErr := filepath.Rel(candidate.Root, absolute)
		outside := relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
		if relErr == nil && !outside && !filepath.IsAbs(relative) && (found.Root == "" || len(candidate.Root) > len(found.Root)) {
			found = candidate
		}
	}
	if found.Root == "" {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return found, nil
}

func (node *Node) Mutate(ctx context.Context, root string, mutate func(*config.Workspace) error) (Workspace, error) {
	workspace, err := node.LoadByRoot(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	if err = mutate(workspace.State); err != nil {
		return Workspace{}, err
	}
	return node.store.Commit(ctx, workspace.Name, workspace.Head, workspace.State, node.identity)
}

func NodeExists() (bool, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(paths.Database)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
