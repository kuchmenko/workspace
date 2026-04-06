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
	Remote   string   `toml:"remote"`
	Path     string   `toml:"path"`
	Status   Status   `toml:"status"`
	Category Category `toml:"category"`
	Group    string   `toml:"group,omitempty"`
	Branches []string `toml:"branches,omitempty"`
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
	return &ws, nil
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
