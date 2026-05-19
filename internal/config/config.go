package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

type MachineConfig struct {
	MachineName string `toml:"machine_name"`
}

var machineNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

func SanitizeMachineName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = machineNameSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func MachineConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ws", "config.toml"), nil
}

func LoadMachineConfig() (*MachineConfig, error) {
	path, err := MachineConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg MachineConfig
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &cfg, nil
	} else if err != nil {
		return nil, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func SaveMachineConfig(cfg *MachineConfig) error {
	path, err := MachineConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func DefaultMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}

	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	s := SanitizeMachineName(h)
	if s == "" {
		return "unknown"
	}
	return s
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
	Remote        string   `toml:"remote"`
	Path          string   `toml:"path"`
	Status        Status   `toml:"status"`
	Category      Category `toml:"category"`
	Group         string   `toml:"group,omitempty"`
	DefaultBranch string   `toml:"default_branch,omitempty"`

	AutoSync *bool `toml:"auto_sync,omitempty"`

	Favorite bool `toml:"favorite,omitempty"`

	Branches []BranchMeta `toml:"branches,omitempty"`

	LegacyAutopush *legacyAutopush `toml:"autopush,omitempty"`
}

func (p Project) SyncEnabled() bool {
	if p.AutoSync == nil {
		return true
	}
	return *p.AutoSync
}

func (p *Project) SetFavorite(fav bool) bool {
	if p.Favorite == fav {
		return false
	}
	p.Favorite = fav
	return true
}

type ValidationKind string

const (
	ValidationDuplicateBranch ValidationKind = "duplicate-branch"
)

type ValidationIssue struct {
	Kind    ValidationKind
	Project string
	Branch  string
	Detail  string
}

func (w *Workspace) Validate() []ValidationIssue {
	var issues []ValidationIssue
	for projName, proj := range w.Projects {
		issues = append(issues, duplicateBranchIssues(projName, proj.Branches)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Project != issues[j].Project {
			return issues[i].Project < issues[j].Project
		}
		return issues[i].Branch < issues[j].Branch
	})
	return issues
}

func duplicateBranchIssues(projName string, branches []BranchMeta) []ValidationIssue {
	seen := make(map[string]int, len(branches))
	var out []ValidationIssue
	for _, b := range branches {
		if b.Name == "" {
			continue
		}
		prev, isDup := seen[b.Name]
		if !isDup {
			seen[b.Name] = len(seen)
			continue
		}
		out = append(out, ValidationIssue{
			Kind:    ValidationDuplicateBranch,
			Project: projName,
			Branch:  b.Name,
			Detail:  fmt.Sprintf("branch %q has %d entries (first at index %d)", b.Name, prev+1, prev),
		})
	}
	return out
}
