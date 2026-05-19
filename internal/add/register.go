package add

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

var ErrAlreadyRegistered = errors.New("project already registered")

type RegisterResult struct {
	Project config.Project
	Name    string
	Cloned  bool
}

func Register(opts Options, url string) (*RegisterResult, error) {
	if opts.WsRoot == "" {
		return nil, errors.New("register: empty WsRoot")
	}
	if opts.Workspace == nil {
		return nil, errors.New("register: nil Workspace")
	}

	name := opts.Name
	if name == "" {
		name = git.ParseRepoName(url)
	}
	if name == "" {
		return nil, fmt.Errorf("register: could not derive project name from %q", url)
	}

	if _, exists := opts.Workspace.Projects[name]; exists {
		return nil, fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}

	cat := opts.Category
	if cat == "" {
		cat = config.CategoryPersonal
	}
	if cat != config.CategoryPersonal && cat != config.CategoryWork {
		return nil, fmt.Errorf("register: category must be personal|work, got %q", cat)
	}

	group := opts.Group
	if group == "" {
		group = inferGroup(url, cat)
	}

	relPath := buildPath(group, cat, name)

	proj := config.Project{
		Remote:   url,
		Path:     relPath,
		Status:   config.StatusActive,
		Category: cat,
		Group:    group,
	}

	cloned := false
	if !opts.NoClone {
		_, err := clone.CloneIntoLayout(opts.WsRoot, name, &proj, clone.Options{})
		if err != nil {
			return nil, fmt.Errorf("clone %s: %w", name, err)
		}
		cloned = true
	}

	if opts.Workspace.Projects == nil {
		opts.Workspace.Projects = make(map[string]config.Project)
	}
	opts.Workspace.Projects[name] = proj

	saveFn := opts.Save
	if saveFn == nil {
		saveFn = func(ws *config.Workspace) error {
			return config.Save(opts.WsRoot, ws)
		}
	}
	if err := saveFn(opts.Workspace); err != nil {
		return nil, fmt.Errorf("save workspace.toml: %w", err)
	}

	return &RegisterResult{Project: proj, Name: name, Cloned: cloned}, nil
}

func inferGroup(_ string, cat config.Category) string {
	return string(cat)
}

func buildPath(group string, cat config.Category, name string) string {
	if group != "" {
		return filepath.Join(group, name)
	}
	return filepath.Join(string(cat), name)
}
