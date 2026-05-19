package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

type Group struct {
	Description string `toml:"description"`

	Favorite bool `toml:"favorite,omitempty"`
}

func (w *Workspace) SetGroupFavorite(name string, fav bool) bool {
	if w.Groups == nil {
		return false
	}
	g, ok := w.Groups[name]
	if !ok {
		return false
	}
	if g.Favorite == fav {
		return false
	}
	g.Favorite = fav
	w.Groups[name] = g
	return true
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
	Agent    AgentConfig        `toml:"agent,omitempty"`
	Daemon   Daemon             `toml:"daemon"`
	Groups   map[string]Group   `toml:"groups"`
	Projects map[string]Project `toml:"projects"`
	Aliases  map[string]string  `toml:"aliases,omitempty"`
}

type AgentConfig struct {
	DefaultView string `toml:"default_view,omitempty"`
}

const (
	AgentViewAll       = "all"
	AgentViewFavorites = "favorites"
)

func (w *Workspace) AgentDefaultView() string {
	switch w.Agent.DefaultView {
	case AgentViewFavorites:
		return AgentViewFavorites
	default:
		return AgentViewAll
	}
}

func (w *Workspace) SetAgentDefaultView(view string) bool {
	var canonical string
	switch view {
	case AgentViewFavorites:
		canonical = AgentViewFavorites
	default:
		canonical = ""
	}
	if w.Agent.DefaultView == canonical {
		return false
	}
	w.Agent.DefaultView = canonical
	return true
}

func FindRoot() (string, error) {
	if env := os.Getenv("WS_ROOT"); env != "" {
		return rootFromEnv(env)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok := rootByWalkUp(dir); ok {
		return root, nil
	}
	return "", fmt.Errorf("workspace.toml not found (set WS_ROOT or run from workspace directory)")
}

func FindRootFrom(start string) (string, bool) {
	if env := os.Getenv("WS_ROOT"); env != "" {
		if _, err := os.Stat(filepath.Join(env, "workspace.toml")); err == nil {
			return env, true
		}
	}
	return rootByWalkUp(start)
}

func rootFromEnv(env string) (string, error) {
	if _, err := os.Stat(filepath.Join(env, "workspace.toml")); err == nil {
		return env, nil
	}
	return "", fmt.Errorf("WS_ROOT=%s does not contain workspace.toml", env)
}

func rootByWalkUp(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "workspace.toml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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
	for name, proj := range ws.Projects {
		migrateLegacyAutopush(&proj)
		ws.Projects[name] = proj
	}
	return &ws, nil
}

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
	cleaned := cleanForSave(ws)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(cleaned)
}

func cleanForSave(ws *Workspace) *Workspace {
	out := *ws
	out.Projects = make(map[string]Project, len(ws.Projects))
	for name, p := range ws.Projects {
		out.Projects[name] = projectForSave(p)
	}
	return &out
}

func projectForSave(p Project) Project {
	p.Branches = filterEmptyMachines(p.Branches)
	p.LegacyAutopush = nil
	return p
}

func filterEmptyMachines(branches []BranchMeta) []BranchMeta {
	if len(branches) == 0 {
		return branches
	}
	kept := make([]BranchMeta, 0, len(branches))
	for _, b := range branches {
		if len(b.Machines) > 0 {
			kept = append(kept, b)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func contains(slice []string, want string) bool {
	for _, v := range slice {
		if v == want {
			return true
		}
	}
	return false
}

func sortedDedup(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
