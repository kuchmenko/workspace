// Package agent implements the TUI launcher for Claude Code sessions
// across workspaces. The UI is a nested list (lazygit-style) with
// inline group expansion, project detail views, and direct session
// launching.
package agent

import (
	"path/filepath"
	"time"
)

type NodeKind int

const (
	KindWorkspace NodeKind = iota
	KindGroup
	KindProject
	KindWorktree
	KindPortal
)

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

func GroupPath(wsRoot, group string) string {
	return filepath.Join(wsRoot, group)
}

type WorkspaceData struct {
	Name           string
	Root           string
	Groups         []string
	Projects       []Project
	FavoriteGroups map[string]bool
}

type Chip struct {
	Kind         NodeKind
	Name         string
	Path         string
	Favorite     bool
	LastActiveAt time.Time

	Project *Project

	WorkspaceRoot string
}
