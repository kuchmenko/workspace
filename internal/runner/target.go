package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/registry"
)

func Resolve(def config.RunnerConfig) (string, error) {
	if def.Path != "" {
		return canonicalDirectory(expandHome(def.Path))
	}
	store, err := registry.OpenDefault()
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByName(context.Background(), def.Workspace)
	if err != nil {
		return "", fmt.Errorf("workspace %q: %w", def.Workspace, err)
	}
	if def.Group != "" {
		path, err := layout.ProjectPath(workspace.Root, def.Group)
		if err != nil {
			return "", err
		}
		return canonicalDirectory(path)
	}
	project, ok := workspace.State.Projects[def.Project]
	if !ok {
		return "", fmt.Errorf("project %q is not registered in workspace %q", def.Project, def.Workspace)
	}
	main, err := layout.ProjectPath(workspace.Root, project.Path)
	if err != nil {
		return "", err
	}
	if def.Worktree == "" {
		return canonicalDirectory(main)
	}
	worktrees, err := git.WorktreeList(layout.BarePath(main))
	if err != nil {
		return "", err
	}
	for _, worktree := range worktrees {
		if worktree.Branch == def.Worktree {
			return canonicalDirectory(worktree.Path)
		}
	}
	return "", fmt.Errorf("worktree %q is not available for project %q", def.Worktree, def.Project)
}

func canonicalDirectory(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"+string(filepath.Separator)))
}

func TargetKey(def config.RunnerConfig) string {
	if def.Path != "" {
		path := filepath.Clean(expandHome(def.Path))
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return "path\x00" + path
	}
	return strings.Join([]string{def.Workspace, def.Group, def.Project, def.Worktree}, "\x00")
}

func FindByTarget(defs []config.RunnerConfig, target config.RunnerConfig) (config.RunnerConfig, bool) {
	key := TargetKey(target)
	for _, def := range defs {
		if TargetKey(def) == key {
			return def, true
		}
	}
	return config.RunnerConfig{}, false
}

func WorkspaceName(workspaces []WorkspaceRef, root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, workspace := range workspaces {
		candidate, candidateErr := filepath.EvalSymlinks(workspace.Root)
		if candidateErr == nil && candidate == resolved {
			return workspace.Name, nil
		}
	}
	return "", errors.New("workspace is not available")
}

type WorkspaceRef struct {
	Name string
	Root string
}
