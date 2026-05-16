package config

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
