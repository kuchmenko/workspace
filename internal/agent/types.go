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
	// KindSection is a non-selectable visual element: the "Favorites"
	// / "Recent" headers above the tree, the "-- all workspaces --"
	// divider beneath them, and the empty-state hint inside an empty
	// Favorites view. Cursor movement skips KindSection rows.
	KindSection
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
	Name     string
	Root     string
	Groups   []string
	Projects []Project
}
