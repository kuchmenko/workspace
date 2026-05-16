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
	// Favorite pins this group to the quick-nav chips of `ws explorer`.
	// Cross-machine — synced via workspace.toml just like project
	// favorites. Toggled by `ws favorite add` / `rm` with a group name
	// or by `f` on a group row in the TUI.
	Favorite bool `toml:"favorite,omitempty"`
}

// SetGroupFavorite flips the named group's Favorite flag. Returns
// true when the in-memory state actually moved. No-op when the group
// is not registered or already in the requested state.
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

// AgentConfig holds workspace-wide user preferences for `ws agent`.
// Synced across machines via workspace.toml. Per-machine preferences
// would live in ~/.config/ws/config.toml instead; AgentConfig is
// intentionally cross-machine.
type AgentConfig struct {
	// DefaultView is the view `ws agent` opens with: "all" (favorites
	// + recent header above the full nested tree) or "favorites" (only
	// the favorites section, flat). Empty string means "all".
	DefaultView string `toml:"default_view,omitempty"`
}

// Agent view enumeration. Stored as the TOML value of agent.default_view.
const (
	AgentViewAll       = "all"
	AgentViewFavorites = "favorites"
)

// AgentDefaultView returns the configured view, falling back to
// AgentViewAll when unset or unrecognized. Callers never need to handle
// the empty-string case.
func (w *Workspace) AgentDefaultView() string {
	switch w.Agent.DefaultView {
	case AgentViewFavorites:
		return AgentViewFavorites
	default:
		return AgentViewAll
	}
}

// SetAgentDefaultView updates agent.default_view. Returns true when the
// in-memory state actually moved. Unknown view values normalize to "all"
// (and are stored as the empty string so the TOML stays compact).
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

// FindRoot walks up from cwd (or uses WS_ROOT env) to find workspace.toml.
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

// FindRootFrom walks up from `start` (an arbitrary absolute path) to the
// filesystem root, returning the first directory that contains
// workspace.toml. Honors the same WS_ROOT env override as FindRoot for
// consistency. Used by `ws agent`'s launch stampers, which receive a
// worktree path that may live anywhere under a workspace.
func FindRootFrom(start string) (string, bool) {
	if env := os.Getenv("WS_ROOT"); env != "" {
		if _, err := os.Stat(filepath.Join(env, "workspace.toml")); err == nil {
			return env, true
		}
	}
	return rootByWalkUp(start)
}

// rootFromEnv validates a WS_ROOT override: returns the path if it
// holds a workspace.toml, otherwise an error explaining which dir
// failed the check (so the user doesn't chase a typo blind).
func rootFromEnv(env string) (string, error) {
	if _, err := os.Stat(filepath.Join(env, "workspace.toml")); err == nil {
		return env, nil
	}
	return "", fmt.Errorf("WS_ROOT=%s does not contain workspace.toml", env)
}

// rootByWalkUp walks upward from `start` to the filesystem root,
// returning the first directory that contains workspace.toml. Returns
// (root, true) on hit; ("", false) when the walk hit the root dir
// without finding one.
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

// Save writes workspace.toml with the new schema. Two cleanup steps run
// before encoding: every BranchMeta with empty Machines is dropped (the
// "no orphan tombstones across save boundaries" invariant), and any
// stray LegacyAutopush field is cleared so the legacy block can never
// round-trip back onto disk.
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

// projectForSave returns a copy of `p` with the on-disk-only invariants
// applied: empty-machines [[branches]] entries are dropped (the orphan-
// tombstone GC), and the legacy autopush field is nil-ed so it never
// round-trips back into workspace.toml after a Load → Save migration.
func projectForSave(p Project) Project {
	p.Branches = filterEmptyMachines(p.Branches)
	p.LegacyAutopush = nil
	return p
}

// filterEmptyMachines drops every BranchMeta whose Machines slice is
// empty. Returns nil when nothing survives so the encoder omits the
// [[branches]] block entirely (rather than emitting an empty array).
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
