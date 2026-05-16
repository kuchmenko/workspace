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

	// Favorite pins this project to the Favorites section of `ws agent`.
	// Cross-machine — synced via workspace.toml. Toggled by `ws favorite
	// add/rm` or the `f` hotkey in the TUI. Race-tolerant by design:
	// concurrent toggles from two machines resolve last-write-wins on the
	// next reconciler tick; the user re-toggles if the wrong side won.
	Favorite bool `toml:"favorite,omitempty"`

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

// updateBranchClaim re-claims an already-registered branch on `machine`:
// adds the machine to the per-branch fleet (idempotent, sorted) and
// bumps last_active_*. Always considered a change because every claim
// is an explicit "I'm active here, now" stamp the cross-machine view
// relies on.
func updateBranchClaim(b *BranchMeta, machine, now string) {
	if !contains(b.Machines, machine) {
		b.Machines = sortedDedup(append(b.Machines, machine))
	}
	b.LastActiveMachine = machine
	b.LastActiveAt = now
}

// ReleaseBranch removes `machine` from the entry's Machines slice. When
// the slice becomes empty the entry is dropped entirely — empty-machines
// blocks never persist across a Save, by acceptance criterion.
//
// Returns (changed, removed). `removed` is true only when the entry was
// dropped from p.Branches.
func (p *Project) ReleaseBranch(name, machine string) (changed bool, removed bool) {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			return p.releaseAt(i, machine)
		}
	}
	return false, false
}

// releaseAt is the per-entry release path: removes `machine` from the
// entry at `idx`, dropping the entry entirely when no machines remain.
// Called by ReleaseBranch after it has located the matching entry.
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
	// Releasing a machine that was the last_active_machine clears the
	// field — the next push or commit on the branch will repopulate
	// it. Keeping a stale machine name there would be misleading.
	if b.LastActiveMachine == machine {
		b.LastActiveMachine = ""
		b.LastActiveAt = ""
	}
	return true, false
}

// removeMachine returns `machines` with all occurrences of `target`
// stripped, plus a flag indicating whether at least one was removed.
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

// StampActivity records "machine just did something on branch `name`
// in this project, right now". Unlike ClaimBranch this is NOT a user-
// driven act of branch creation, so CreatedBy/CreatedAt are intentionally
// left untouched: a freshly stamped main-branch entry must not pretend
// the current machine created `main`. Used by `ws agent`'s shell/claude
// launchers to make every launch into a worktree count toward the
// project's last-activity timestamp (computed as max over branches).
//
// If the branch entry exists: bumps LastActive* and adds `machine` to
// Machines if missing. If absent: creates a minimal entry carrying only
// the activity fields.
//
// Returns true when in-memory state moved.
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

// SetFavorite flips this project's Favorite flag. Returns true when the
// in-memory state actually moved. Idempotent: setting true on an
// already-favorited project (or false on a non-favorited one) is a no-op
// and returns false.
func (p *Project) SetFavorite(fav bool) bool {
	if p.Favorite == fav {
		return false
	}
	p.Favorite = fav
	return true
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

// duplicateBranchIssues reports duplicate-name [[branches]] entries
// within one project. The first occurrence is tracked silently; every
// subsequent occurrence yields a ValidationIssue.
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
	for _, o := range p.LegacyAutopush.Owned {
		p.appendLegacyOwned(o)
	}
	for _, name := range p.LegacyAutopush.Branches {
		p.appendLegacyBare(name)
	}
}

// appendLegacyOwned converts one [[autopush.owned]] entry into the
// new [[branches]] shape. Owned entries always carry machine
// attribution and are always known-pushed (the legacy daemon pushed
// them by definition), so the migration sets every metadata field.
// Idempotent: re-loads of an already-migrated workspace.toml skip
// any branch that already has a [[branches]] entry.
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

// appendLegacyBare converts one autopush.branches []string entry into
// a placeholder [[branches]] block with empty Machines. Save's empty-
// machines GC drops it on the next write — the user loses no actual
// git data because the underlying ref is still in the bare repo, and
// `ws worktree add` re-registers it properly when the user picks it up.
func (p *Project) appendLegacyBare(name string) {
	if name == "" || p.LookupBranch(name) != nil {
		return
	}
	p.Branches = append(p.Branches, BranchMeta{Name: name})
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
