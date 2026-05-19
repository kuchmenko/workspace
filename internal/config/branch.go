package config

import "time"

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
