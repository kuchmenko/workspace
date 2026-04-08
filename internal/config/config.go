package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
//
// Two formats coexist:
//
//   - Branches: legacy flat list, no machine attribution. Read by the
//     daemon for push decisions. New writes go to Owned instead.
//   - Owned: per-branch ownership records with the machine that
//     promoted/created the branch. Used both by the daemon (push
//     decisions) and by `ws pulse` (machine attribution for events
//     on branches that don't carry the wt/<machine>/* prefix).
type Autopush struct {
	Branches []string       `toml:"branches,omitempty"`
	Owned    []OwnedBranch  `toml:"owned,omitempty"`
}

// OwnedBranch records that a non-wt/* branch in this project is owned
// by a specific machine. Created by `ws worktree new --branch X
// --auto-push` and by `ws worktree promote`. The owning machine is
// the one whose worktree dir holds the branch's checkout.
type OwnedBranch struct {
	Branch  string `toml:"branch"`
	Machine string `toml:"machine"`
	// Since is the RFC3339 timestamp of when this machine claimed the
	// branch. Used by pulse to split events on a single branch across
	// machines after a `--reclaim`.
	Since string `toml:"since,omitempty"`
}

// OwnerOf returns the machine that owns `branch` for this project, or
// "" if the branch has no recorded owner. Pulse uses this as the
// fallback machine resolver for branches outside the wt/<machine>/*
// namespace.
func (p Project) OwnerOf(branch string) string {
	if p.Autopush == nil {
		return ""
	}
	for _, o := range p.Autopush.Owned {
		if o.Branch == branch {
			return o.Machine
		}
	}
	return ""
}

// ErrBranchOwnedByOther is returned by ClaimAutopushBranch when the
// branch already has a recorded owner on a different machine and the
// caller did not pass reclaim=true. The CLI surfaces this as a
// suggestion to retry with --reclaim.
type ErrBranchOwnedByOther struct {
	Branch    string
	Owner     string
	Requested string
}

func (e *ErrBranchOwnedByOther) Error() string {
	return fmt.Sprintf("branch %q is already owned by machine %q (this machine: %q); pass --reclaim to take ownership",
		e.Branch, e.Owner, e.Requested)
}

// ClaimAutopushBranch records that `machine` owns `branch` for this
// project. Returns true if the registry actually changed.
//
// If the branch already has an owner equal to `machine`, this is a
// no-op. If the owner is a different machine and reclaim=false, the
// call returns *ErrBranchOwnedByOther without modifying state. With
// reclaim=true, the previous owner is overwritten and Since is reset
// to the current time.
func (p *Project) ClaimAutopushBranch(branch, machine string, reclaim bool) (bool, error) {
	if branch == "" || machine == "" {
		return false, nil
	}
	if p.Autopush == nil {
		p.Autopush = &Autopush{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, o := range p.Autopush.Owned {
		if o.Branch != branch {
			continue
		}
		if o.Machine == machine {
			return false, nil
		}
		if !reclaim {
			return false, &ErrBranchOwnedByOther{Branch: branch, Owner: o.Machine, Requested: machine}
		}
		p.Autopush.Owned[i].Machine = machine
		p.Autopush.Owned[i].Since = now
		return true, nil
	}
	p.Autopush.Owned = append(p.Autopush.Owned, OwnedBranch{Branch: branch, Machine: machine, Since: now})
	return true, nil
}

// ReleaseAutopushBranch removes any ownership record for branch in
// this project, plus any legacy entry in Branches. Returns true if
// anything changed.
func (p *Project) ReleaseAutopushBranch(branch string) bool {
	if p.Autopush == nil {
		return false
	}
	changed := false
	for i, o := range p.Autopush.Owned {
		if o.Branch == branch {
			p.Autopush.Owned = append(p.Autopush.Owned[:i], p.Autopush.Owned[i+1:]...)
			changed = true
			break
		}
	}
	for i, b := range p.Autopush.Branches {
		if b == branch {
			p.Autopush.Branches = append(p.Autopush.Branches[:i], p.Autopush.Branches[i+1:]...)
			changed = true
			break
		}
	}
	if changed && len(p.Autopush.Owned) == 0 && len(p.Autopush.Branches) == 0 {
		p.Autopush = nil
	}
	return changed
}

// AutopushAllows reports whether the reconciler should treat `branch`
// as explicitly opted in to auto-push. Only matters for branches that
// do NOT match the wt/<machine>/ prefix — those are always pushed.
// Reads both the legacy Branches list and the new Owned registry.
func (p Project) AutopushAllows(branch string) bool {
	if p.Autopush == nil {
		return false
	}
	for _, b := range p.Autopush.Branches {
		if b == branch {
			return true
		}
	}
	for _, o := range p.Autopush.Owned {
		if o.Branch == branch {
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
