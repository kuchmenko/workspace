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

// SyncEnabled reports whether the reconciler should push/pull this project.
// Defaults to true when the field is unset.
func (p Project) SyncEnabled() bool {
	if p.AutoSync == nil {
		return true
	}
	return *p.AutoSync
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
