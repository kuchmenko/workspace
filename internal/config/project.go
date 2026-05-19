package config

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
