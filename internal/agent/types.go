// Package agent implements the TUI launcher for Claude Code sessions
// across workspaces. The UI is a nested list (lazygit-style) with
// inline group expansion, project detail views, and direct session
// launching.
package agent

import (
	"path/filepath"
	"time"
)

// NodeKind classifies an item in the workspace tree.
type NodeKind int

const (
	KindWorkspace NodeKind = iota
	KindGroup
	KindProject
	KindWorktree
	KindPortal
)

// Project is one navigable project in the workspace tree.
//
// Favorite / LastActiveAt / LastActiveMachine are populated from
// workspace.toml at LoadWorkspaces time. LastActiveAt is the most
// recent timestamp across all of the project's [[branches]] entries
// (which currently includes the project's default branch once the
// `ws agent` launcher has stamped it). Zero time = no activity ever
// recorded — such projects never appear in the Recent header section.
type Project struct {
	ID                string
	Name              string
	Group             string
	Category          string
	Path              string
	DefaultBranch     string
	WorktreeCount     int
	SessionCount      int
	Favorite          bool
	LastActiveAt      time.Time
	LastActiveMachine string
}

// GroupPath returns the filesystem directory for a group under a
// workspace root. E.g. root="/home/user/development", group="work"
// → "/home/user/development/work".
func GroupPath(wsRoot, group string) string {
	return filepath.Join(wsRoot, group)
}

// Workspace is the top-level data structure loaded from workspace.toml
// and daemon.toml, used by the TUI.
type WorkspaceData struct {
	Name           string
	Root           string
	Groups         []string
	Projects       []Project
	FavoriteGroups map[string]bool // group name → pinned to header chips
}

// Chip is one entry in the pinned quick-nav header. Either a project
// (Project != nil) or a group (Group != ""); never both. Chips can
// represent favorites from either kind, plus recently-touched
// non-favorite projects.
type Chip struct {
	Kind         NodeKind // KindProject or KindGroup
	Name         string   // display name
	Path         string   // cwd to launch in
	Favorite     bool
	LastActiveAt time.Time
	// Project is set when Kind == KindProject. Groups do not carry
	// per-row metadata beyond name and path so this is nil for them.
	Project *Project
	// WorkspaceRoot is the root of the workspace this chip belongs to.
	// Needed so toggleFavoriteFor can resolve which workspace.toml to
	// mutate when the chip is a group.
	WorkspaceRoot string
}
