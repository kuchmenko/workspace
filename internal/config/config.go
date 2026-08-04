package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

type BranchMeta struct {
	Name              string   `toml:"name"`
	Machines          []string `toml:"machines,omitempty"`
	LastActiveMachine string   `toml:"last_active_machine,omitempty"`
	LastActiveAt      string   `toml:"last_active_at,omitempty"`

	LastPushedMachine string `toml:"last_pushed_machine,omitempty"`
	LastPushedAt      string `toml:"last_pushed_at,omitempty"`
	CreatedBy         string `toml:"created_by,omitempty"`
	CreatedAt         string `toml:"created_at,omitempty"`
}

func (p *Project) LookupBranch(name string) *BranchMeta {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			return &p.Branches[i]
		}
	}
	return nil
}

func (p *Project) ClaimBranch(name, machine string) (changed bool, isNew bool) {
	if name == "" || machine == "" {
		return false, false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if b := p.LookupBranch(name); b != nil {
		updateBranchClaim(b, machine, now)
		return true, false
	}
	p.Branches = append(p.Branches, BranchMeta{
		Name:              name,
		Machines:          []string{machine},
		LastActiveMachine: machine,
		LastActiveAt:      now,
		CreatedBy:         machine,
		CreatedAt:         now,
	})
	return true, true
}

func updateBranchClaim(b *BranchMeta, machine, now string) {
	if !contains(b.Machines, machine) {
		b.Machines = sortedDedup(append(b.Machines, machine))
	}
	b.LastActiveMachine = machine
	b.LastActiveAt = now
}

func (p *Project) ReleaseBranch(name, machine string) (changed bool, removed bool) {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			return p.releaseAt(i, machine)
		}
	}
	return false, false
}

func (p *Project) releaseAt(idx int, machine string) (changed bool, removed bool) {
	b := &p.Branches[idx]
	filtered, dropped := removeMachine(b.Machines, machine)
	if !dropped {
		return false, false
	}
	if len(filtered) == 0 {
		p.Branches = append(p.Branches[:idx], p.Branches[idx+1:]...)
		return true, true
	}
	b.Machines = filtered

	if b.LastActiveMachine == machine {
		b.LastActiveMachine = ""
		b.LastActiveAt = ""
	}
	return true, false
}

func removeMachine(machines []string, target string) (filtered []string, dropped bool) {
	out := make([]string, 0, len(machines))
	for _, m := range machines {
		if m == target {
			dropped = true
			continue
		}
		out = append(out, m)
	}
	return out, dropped
}

func (p *Project) TouchActive(name, machine string, when time.Time) bool {
	b := p.LookupBranch(name)
	if b == nil {
		return false
	}
	stamp := when.UTC().Format(time.RFC3339)
	if b.LastActiveMachine == machine && b.LastActiveAt == stamp {
		return false
	}
	b.LastActiveMachine = machine
	b.LastActiveAt = stamp
	return true
}

func (p *Project) StampActivity(name, machine string, when time.Time) bool {
	if name == "" || machine == "" {
		return false
	}
	stamp := when.UTC().Format(time.RFC3339)
	if b := p.LookupBranch(name); b != nil {
		changed := false
		if !contains(b.Machines, machine) {
			b.Machines = sortedDedup(append(b.Machines, machine))
			changed = true
		}
		if b.LastActiveMachine != machine || b.LastActiveAt != stamp {
			b.LastActiveMachine = machine
			b.LastActiveAt = stamp
			changed = true
		}
		return changed
	}
	p.Branches = append(p.Branches, BranchMeta{
		Name:              name,
		Machines:          []string{machine},
		LastActiveMachine: machine,
		LastActiveAt:      stamp,
	})
	return true
}

func (p *Project) RemoveBranch(name string) bool {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			p.Branches = append(p.Branches[:i], p.Branches[i+1:]...)
			return true
		}
	}
	return false
}

func (p *Project) MarkPushed(name, machine string, when time.Time) bool {
	b := p.LookupBranch(name)
	if b == nil {
		return false
	}
	stamp := when.UTC().Format(time.RFC3339)
	if b.LastPushedMachine == machine && b.LastPushedAt == stamp &&
		b.LastActiveMachine == machine && b.LastActiveAt == stamp {
		return false
	}
	b.LastPushedMachine = machine
	b.LastPushedAt = stamp
	b.LastActiveMachine = machine
	b.LastActiveAt = stamp
	return true
}

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

type Workspace struct {
	Meta     Meta               `toml:"meta"`
	Agent    AgentConfig        `toml:"agent,omitempty"`
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
		Groups:   make(map[string]Group),
		Projects: make(map[string]Project),
		Aliases:  make(map[string]string),
	}
	return ws, nil
}

func Save(root string, ws *Workspace) error {
	path := filepath.Join(root, "workspace.toml")
	target, mode, err := workspaceSaveTarget(path)
	if err != nil {
		return err
	}
	tmp, err := encodeWorkspaceTemp(target, mode, ws)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = dir.Close()
		return err
	}
	_ = dir.Sync()
	_ = dir.Close()
	return nil
}

func workspaceSaveTarget(path string) (string, os.FileMode, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		if info, lstatErr := os.Lstat(path); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", 0, fmt.Errorf("resolve workspace.toml symlink: %w", err)
		}
		if !os.IsNotExist(err) {
			return "", 0, err
		}
		target = path
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(target); statErr == nil {
		mode = info.Mode()
	} else if !os.IsNotExist(statErr) {
		return "", 0, statErr
	}
	return target, mode, nil
}

func encodeWorkspaceTemp(target string, mode os.FileMode, ws *Workspace) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), ".workspace.toml-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return "", err
	}
	cleaned := cleanForSave(ws)
	if err := toml.NewEncoder(f).Encode(cleaned); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	var verified Workspace
	if _, err := toml.DecodeFile(tmp, &verified); err != nil {
		return "", fmt.Errorf("verify encoded workspace.toml: %w", err)
	}
	failed = false
	return tmp, nil
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

type legacyAutopush struct {
	Branches []string            `toml:"branches,omitempty"`
	Owned    []legacyOwnedBranch `toml:"owned,omitempty"`
}

type legacyOwnedBranch struct {
	Branch  string `toml:"branch"`
	Machine string `toml:"machine"`
	Since   string `toml:"since,omitempty"`
}

func migrateLegacyAutopush(p *Project) {
	if p.LegacyAutopush == nil {
		return
	}
	defer func() { p.LegacyAutopush = nil }()
	for _, o := range p.LegacyAutopush.Owned {
		p.appendLegacyOwned(o)
	}
	for _, name := range p.LegacyAutopush.Branches {
		p.appendLegacyBare(name)
	}
}

func (p *Project) appendLegacyOwned(o legacyOwnedBranch) {
	if o.Branch == "" || p.LookupBranch(o.Branch) != nil {
		return
	}
	machines := []string{}
	if o.Machine != "" {
		machines = []string{o.Machine}
	}
	p.Branches = append(p.Branches, BranchMeta{
		Name:              o.Branch,
		Machines:          machines,
		LastActiveMachine: o.Machine,
		LastActiveAt:      o.Since,
		LastPushedMachine: o.Machine,
		LastPushedAt:      o.Since,
		CreatedBy:         o.Machine,
		CreatedAt:         o.Since,
	})
}

func (p *Project) appendLegacyBare(name string) {
	if name == "" || p.LookupBranch(name) != nil {
		return
	}
	p.Branches = append(p.Branches, BranchMeta{Name: name})
}

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
	Remote        string            `toml:"remote"`
	Mirrors       map[string]string `toml:"mirrors,omitempty"`
	Path          string            `toml:"path"`
	Status        Status            `toml:"status"`
	Category      Category          `toml:"category"`
	Group         string            `toml:"group,omitempty"`
	DefaultBranch string            `toml:"default_branch,omitempty"`

	Favorite bool `toml:"favorite,omitempty"`

	Branches []BranchMeta `toml:"branches,omitempty"`

	LegacyAutopush *legacyAutopush `toml:"autopush,omitempty"`
}

func (p *Project) SetFavorite(fav bool) bool {
	if p.Favorite == fav {
		return false
	}
	p.Favorite = fav
	return true
}
