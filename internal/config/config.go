package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
	StatusDormant  Status = "dormant"
)

type Category string

const (
	CategoryPersonal Category = "personal"
	CategoryWork     Category = "work"
)

type Project struct {
	Remote        string   `toml:"remote"`
	Path          string   `toml:"path"`
	Status        Status   `toml:"status"`
	Category      Category `toml:"category"`
	Group         string   `toml:"group,omitempty"`
	Branches      []string `toml:"branches,omitempty"`
	DefaultBranch string   `toml:"default_branch,omitempty"`
	// AutoSync controls per-project sync behavior. nil = inherit (default true).
	// Pointer so we can distinguish "unset" from "explicitly false" in TOML.
	AutoSync *bool `toml:"auto_sync,omitempty"`

	// BranchNaming describes the repository-native branch convention used
	// when promoting a wt/<machine>/<topic> WIP branch into its final form
	// (e.g. "feat/fix-login") before opening a PR. Optional — when unset,
	// `ws worktree promote` requires an explicit --name.
	BranchNaming *BranchNaming `toml:"branch_naming,omitempty"`

	// Autopush lists non-wt/* branches that the daemon is explicitly
	// allowed to push for this project. Populated by `ws worktree new
	// --auto-push <name>` and by `ws worktree promote --auto-push`.
	// The wt/<machine>/* prefix is always pushed regardless of this list.
	Autopush *Autopush `toml:"autopush,omitempty"`
}

// BranchNaming holds the per-project branch-name convention used by
// `ws worktree promote`. Pattern supports a single placeholder: {topic}.
type BranchNaming struct {
	// Pattern is the template applied to the worktree's topic when no
	// explicit --name is given to promote. Example: "feat/{topic}".
	Pattern string `toml:"pattern,omitempty"`
	// Validate is an optional regex the final branch name must match.
	// Only checked at promote time; WIP names (wt/<machine>/*) are never
	// validated against it.
	Validate string `toml:"validate,omitempty"`
}

// Autopush is the opt-in list of non-wt/* branches the daemon may push
// for a project. Stored in workspace.toml so it syncs across machines.
type Autopush struct {
	Branches []string `toml:"branches,omitempty"`
}

// AddAutopushBranch adds branch to the project's autopush list if not
// already present. Returns true if the list changed.
func (p *Project) AddAutopushBranch(branch string) bool {
	if branch == "" {
		return false
	}
	if p.Autopush == nil {
		p.Autopush = &Autopush{}
	}
	for _, b := range p.Autopush.Branches {
		if b == branch {
			return false
		}
	}
	p.Autopush.Branches = append(p.Autopush.Branches, branch)
	return true
}

// RemoveAutopushBranch removes branch from the project's autopush list.
// Returns true if the list changed.
func (p *Project) RemoveAutopushBranch(branch string) bool {
	if p.Autopush == nil {
		return false
	}
	for i, b := range p.Autopush.Branches {
		if b == branch {
			p.Autopush.Branches = append(p.Autopush.Branches[:i], p.Autopush.Branches[i+1:]...)
			if len(p.Autopush.Branches) == 0 {
				p.Autopush = nil
			}
			return true
		}
	}
	return false
}

// AutopushAllows reports whether the reconciler should treat `branch`
// as explicitly opted in to auto-push. Only matters for branches that
// do NOT match the wt/<machine>/ prefix — those are always pushed.
func (p Project) AutopushAllows(branch string) bool {
	if p.Autopush == nil {
		return false
	}
	for _, b := range p.Autopush.Branches {
		if b == branch {
			return true
		}
	}
	return false
}

// SyncEnabled reports whether the reconciler should push/pull this project.
// Defaults to true when the field is unset.
func (p Project) SyncEnabled() bool {
	if p.AutoSync == nil {
		return true
	}
	return *p.AutoSync
}

type Group struct {
	Description string `toml:"description"`
}

type Meta struct {
	Version int    `toml:"version"`
	Root    string `toml:"root"`
}

type Daemon struct {
	PollInterval   string `toml:"poll_interval"`
	StaleThreshold string `toml:"stale_threshold"`
	AutoSync       bool   `toml:"auto_sync"`
	WatchDirs      bool   `toml:"watch_dirs"`
}

type Workspace struct {
	Meta     Meta               `toml:"meta"`
	Daemon   Daemon             `toml:"daemon"`
	Groups   map[string]Group   `toml:"groups"`
	Projects map[string]Project `toml:"projects"`
	Aliases  map[string]string  `toml:"aliases,omitempty"`
}

// FindRoot walks up from cwd (or uses WS_ROOT env) to find workspace.toml.
func FindRoot() (string, error) {
	if env := os.Getenv("WS_ROOT"); env != "" {
		if _, err := os.Stat(filepath.Join(env, "workspace.toml")); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("WS_ROOT=%s does not contain workspace.toml", env)
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "workspace.toml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("workspace.toml not found (set WS_ROOT or run from workspace directory)")
}

func Load(root string) (*Workspace, error) {
	path := filepath.Join(root, "workspace.toml")
	var ws Workspace
	if _, err := toml.DecodeFile(path, &ws); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if ws.Projects == nil {
		ws.Projects = make(map[string]Project)
	}
	if ws.Groups == nil {
		ws.Groups = make(map[string]Group)
	}
	if ws.Aliases == nil {
		ws.Aliases = make(map[string]string)
	}
	return &ws, nil
}

// LoadOrCreate loads workspace.toml if it exists, otherwise creates a default one.
func LoadOrCreate(root string) (*Workspace, error) {
	path := filepath.Join(root, "workspace.toml")
	if _, err := os.Stat(path); err == nil {
		return Load(root)
	}
	ws := &Workspace{
		Meta:     Meta{Version: 1, Root: root},
		Daemon:   Daemon{PollInterval: "5m", StaleThreshold: "30d", AutoSync: true, WatchDirs: true},
		Groups:   make(map[string]Group),
		Projects: make(map[string]Project),
		Aliases:  make(map[string]string),
	}
	return ws, nil
}

func Save(root string, ws *Workspace) error {
	path := filepath.Join(root, "workspace.toml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(ws)
}
