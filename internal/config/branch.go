package config

import "time"

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
