package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	DefaultBranch string   `toml:"default_branch,omitempty"`
	// AutoSync controls per-project sync behavior. nil = inherit (default true).
	// Pointer so we can distinguish "unset" from "explicitly false" in TOML.
	AutoSync *bool `toml:"auto_sync,omitempty"`

	// Branches holds the per-branch metadata that travels with the project
	// across machines. Replaces the legacy [[autopush.owned]] table; see
	// migrateLegacyAutopush for the on-load translation.
	Branches []BranchMeta `toml:"branches,omitempty"`

	// LegacyAutopush is the pre-0.7.0 [[autopush]] block. Read-only at Load
	// time — migrateLegacyAutopush folds its contents into Branches and
	// Save unconditionally drops the field.
	LegacyAutopush *legacyAutopush `toml:"autopush,omitempty"`
}

// BranchMeta carries the per-branch state for a project: which machines
// hold a local worktree, when this project last saw activity on the
// branch, and where it originated. Stored as [[projects.X.branches]]
// in workspace.toml. The array-of-tables shape is critical: union-merge
// on workspace.toml concatenates these blocks cleanly when two machines
// add different branches in parallel.
type BranchMeta struct {
	Name              string   `toml:"name"`
	Machines          []string `toml:"machines,omitempty"`
	LastActiveMachine string   `toml:"last_active_machine,omitempty"`
	LastActiveAt      string   `toml:"last_active_at,omitempty"`
	// LastPushedMachine and LastPushedAt are written only when the
	// branch is observed on origin — either by `ws worktree push`
	// (after a successful push) or by `ws worktree add` attaching
	// to an already-existing remote branch. They are the orphan-
	// detection signal: the reconciler only treats a branch as
	// "should exist on origin" if at least one machine has pushed
	// it. A locally-created branch with no pushes never trips
	// branch-orphan even though LastActiveAt is set on add.
	LastPushedMachine string `toml:"last_pushed_machine,omitempty"`
	LastPushedAt      string `toml:"last_pushed_at,omitempty"`
	CreatedBy         string `toml:"created_by,omitempty"`
	CreatedAt         string `toml:"created_at,omitempty"`
}

// legacyAutopush is the pre-0.7.0 schema, kept only for Load-time
// migration. New code reads/writes Project.Branches.
type legacyAutopush struct {
	Branches []string            `toml:"branches,omitempty"`
	Owned    []legacyOwnedBranch `toml:"owned,omitempty"`
}

type legacyOwnedBranch struct {
	Branch  string `toml:"branch"`
	Machine string `toml:"machine"`
	Since   string `toml:"since,omitempty"`
}

// LookupBranch returns a pointer to the entry for `name`, or nil if the
// branch is unknown to this project. The pointer aliases the underlying
// slice element — mutations through it modify the project's state.
func (p *Project) LookupBranch(name string) *BranchMeta {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			return &p.Branches[i]
		}
	}
	return nil
}

// ClaimBranch records that `machine` currently holds a local worktree
// of `name` in this project. On first claim it also sets CreatedBy and
// CreatedAt so the original creator is preserved across handoffs. On
// every claim it bumps LastActiveMachine / LastActiveAt to (machine,
// now), reflecting that this machine just became active on the branch.
//
// Returns (changed, isNew). `changed` is true when the in-memory state
// actually moved; `isNew` is true when this call created the entry.
func (p *Project) ClaimBranch(name, machine string) (changed bool, isNew bool) {
	if name == "" || machine == "" {
		return false, false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if b := p.LookupBranch(name); b != nil {
		// Existing entry: append machine if missing, bump activity.
		appended := false
		if !contains(b.Machines, machine) {
			b.Machines = sortedDedup(append(b.Machines, machine))
			appended = true
		}
		if b.LastActiveMachine != machine || b.LastActiveAt == "" {
			b.LastActiveMachine = machine
			b.LastActiveAt = now
			return true, false
		}
		if appended {
			b.LastActiveAt = now
			return true, false
		}
		// Same machine, same active state → still bump LastActiveAt so
		// "I just added a worktree" is observable.
		b.LastActiveAt = now
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

// ReleaseBranch removes `machine` from the entry's Machines slice. When
// the slice becomes empty the entry is dropped entirely — empty-machines
// blocks never persist across a Save, by acceptance criterion.
//
// Returns (changed, removed). `removed` is true only when the entry was
// dropped from p.Branches.
func (p *Project) ReleaseBranch(name, machine string) (changed bool, removed bool) {
	for i := range p.Branches {
		if p.Branches[i].Name != name {
			continue
		}
		machines := p.Branches[i].Machines
		filtered := make([]string, 0, len(machines))
		dropped := false
		for _, m := range machines {
			if m == machine {
				dropped = true
				continue
			}
			filtered = append(filtered, m)
		}
		if !dropped {
			return false, false
		}
		if len(filtered) == 0 {
			p.Branches = append(p.Branches[:i], p.Branches[i+1:]...)
			return true, true
		}
		p.Branches[i].Machines = filtered
		// Releasing a machine that was the last_active_machine clears the
		// field — the next push or commit on the branch will repopulate
		// it. Keeping a stale machine name there would be misleading.
		if p.Branches[i].LastActiveMachine == machine {
			p.Branches[i].LastActiveMachine = ""
			p.Branches[i].LastActiveAt = ""
		}
		return true, false
	}
	return false, false
}

// TouchActive bumps LastActiveMachine / LastActiveAt for `name`. No-op
// if the branch is not registered. Returns true when state changed.
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

// RemoveBranch drops the entry for `name` from this project's Branches
// slice unconditionally. Returns true if an entry was removed. Used by
// `ws sync resolve` to clean up branch-orphan entries on machines that
// never had a local worktree on the orphaned branch — ReleaseBranch
// would no-op there because the machine isn't in `Machines` to begin
// with, leaving the entry (and its `last_pushed_*` trigger) in place.
func (p *Project) RemoveBranch(name string) bool {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			p.Branches = append(p.Branches[:i], p.Branches[i+1:]...)
			return true
		}
	}
	return false
}

// MarkPushed records that `machine` published `name` to origin at `when`.
// Also bumps LastActiveMachine / LastActiveAt because a push is an
// activity. No-op if the branch is not registered. Returns true when
// state changed.
//
// The push fields are the orphan-detection signal: they distinguish
// "this branch was on origin and should still be" (push fields set →
// origin disappearance is meaningful) from "this branch is brand-new
// and never published" (push fields empty → origin absence is normal).
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

// ValidationKind enumerates the structural problems Validate can detect.
type ValidationKind string

const (
	ValidationDuplicateBranch ValidationKind = "duplicate-branch"
)

// ValidationIssue describes one Workspace structural defect found by
// Validate. Callers (notably the reconciler) translate these into
// conflict-store entries (KindBranchDuplicate).
type ValidationIssue struct {
	Kind    ValidationKind
	Project string
	Branch  string
	Detail  string
}

// Validate inspects the in-memory Workspace for structural defects that
// the TOML decoder will not catch on its own — currently: duplicate
// branch names within a project's [[branches]] list, which arise when
// two machines independently add the same branch and union-merge
// concatenates their writes.
func (w *Workspace) Validate() []ValidationIssue {
	var issues []ValidationIssue
	for projName, proj := range w.Projects {
		seen := make(map[string]int, len(proj.Branches))
		for _, b := range proj.Branches {
			if b.Name == "" {
				continue
			}
			if prev, ok := seen[b.Name]; ok {
				issues = append(issues, ValidationIssue{
					Kind:    ValidationDuplicateBranch,
					Project: projName,
					Branch:  b.Name,
					Detail:  fmt.Sprintf("branch %q has %d entries (first at index %d)", b.Name, prev+1, prev),
				})
				continue
			}
			seen[b.Name] = len(seen)
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Project != issues[j].Project {
			return issues[i].Project < issues[j].Project
		}
		return issues[i].Branch < issues[j].Branch
	})
	return issues
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
	for name, proj := range ws.Projects {
		migrateLegacyAutopush(&proj)
		ws.Projects[name] = proj
	}
	return &ws, nil
}

// migrateLegacyAutopush folds a project's [[autopush.owned]] entries and
// autopush.branches []string list into Project.Branches, then nils out
// the legacy field so subsequent saves never re-emit it.
//
// Migration is idempotent: a project with no legacy data is untouched;
// a project whose [[branches]] already exists keeps its current entries
// while still picking up any new legacy rows that pre-date the upgrade.
//
// autopush.branches []string entries (no machine attribution) become
// BranchMeta with empty Machines. The Save GC drops them on the next
// write — the user loses no actual git data because the underlying ref
// is still in the bare repo and `ws worktree add` re-registers it
// properly when the user next picks it up.
func migrateLegacyAutopush(p *Project) {
	if p.LegacyAutopush == nil {
		return
	}
	defer func() { p.LegacyAutopush = nil }()

	// Owned entries carry machine attribution → become full BranchMeta.
	for _, o := range p.LegacyAutopush.Owned {
		if o.Branch == "" {
			continue
		}
		if existing := p.LookupBranch(o.Branch); existing != nil {
			// Already migrated on a previous load; preserve the new entry.
			continue
		}
		machines := []string{}
		if o.Machine != "" {
			machines = []string{o.Machine}
		}
		// Legacy autopush.owned entries were always pushed by definition
		// (the daemon pushed them). Carry that signal forward to the
		// new push fields so the reconciler's orphan check treats them
		// correctly post-migration.
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

	// Bare branches []string have no machine attribution → BranchMeta
	// with empty Machines. These are GC'd on the next Save unless a
	// `ws worktree add` claims them first.
	for _, name := range p.LegacyAutopush.Branches {
		if name == "" {
			continue
		}
		if existing := p.LookupBranch(name); existing != nil {
			continue
		}
		p.Branches = append(p.Branches, BranchMeta{Name: name})
	}
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
		if len(p.Branches) > 0 {
			kept := make([]BranchMeta, 0, len(p.Branches))
			for _, b := range p.Branches {
				if len(b.Machines) > 0 {
					kept = append(kept, b)
				}
			}
			if len(kept) == 0 {
				p.Branches = nil
			} else {
				p.Branches = kept
			}
		}
		p.LegacyAutopush = nil
		out.Projects[name] = p
	}
	return &out
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
