// Package agent implements the TUI launcher for Claude Code sessions
// across workspaces. The UI is a nested list (lazygit-style) with
// inline group expansion, project detail views, and direct session
// launching.
package agent

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
type Project struct {
	ID            string
	Name          string
	Group         string
	Category      string
	Path          string
	DefaultBranch string
	WorktreeCount int
	SessionCount  int
}

// Workspace is the top-level data structure loaded from workspace.toml
// and daemon.toml, used by the TUI.
type WorkspaceData struct {
	Name     string
	Root     string
	Groups   []string
	Projects []Project
}
