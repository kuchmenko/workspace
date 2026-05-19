package config

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
