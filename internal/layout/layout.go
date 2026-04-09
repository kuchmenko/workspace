// Package layout computes the canonical filesystem paths for the
// worktree-based project layout. Centralized so that migrate, reconciler,
// archive, restore, scan and clean all agree on where things live.
//
// For a project with workspace-relative path "personal/myapp" the layout is:
//
//	<wsRoot>/personal/myapp           ← main worktree (default branch)
//	<wsRoot>/personal/myapp.bare      ← bare repo, source of truth
//	<wsRoot>/personal/myapp-wt-<machine>-<topic>  ← extra worktrees
//
// All helpers in this package operate on absolute paths. Callers are expected
// to have already joined the workspace root with the project's relative path.
package layout

import (
	"path/filepath"
	"strings"
)

// BarePath returns the absolute path to the bare repo for a project whose
// main worktree lives at mainWorktree. The bare repo is a sibling with a
// `.bare` suffix on the basename.
func BarePath(mainWorktree string) string {
	return mainWorktree + ".bare"
}

// WorktreeDirName builds the filesystem-safe directory name for an extra
// worktree of the given project. The directory lives as a sibling of the
// main worktree.
//
// Example: project "myapp", machine "asahi", topic "auth/refactor" →
//
//	"myapp-wt-asahi-auth-refactor"
//
// Slashes in the topic are flattened to dashes so the result is a single
// path segment that can sit next to "myapp" and "myapp.bare".
func WorktreeDirName(projectBaseName, machine, topic string) string {
	safeTopic := strings.ReplaceAll(topic, "/", "-")
	return projectBaseName + "-wt-" + machine + "-" + safeTopic
}

// WorktreePath returns the absolute path of an extra worktree given the main
// worktree path and the worktree's machine + topic.
func WorktreePath(mainWorktree, machine, topic string) string {
	dir := filepath.Dir(mainWorktree)
	base := filepath.Base(mainWorktree)
	return filepath.Join(dir, WorktreeDirName(base, machine, topic))
}

// BranchName builds the canonical wt/<machine>/<topic> branch name. The
// topic keeps its slashes — only filesystem paths get flattened.
func BranchName(machine, topic string) string {
	return "wt/" + machine + "/" + topic
}

// SlugifyBranch converts a branch name to a filesystem-safe directory
// component: slashes → dashes, strip leading/trailing dashes. Used when
// --branch overrides the topic to derive the worktree directory name
// from the branch instead.
//
//	"feat/buddy" → "feat-buddy"
//	"fix/amm-prices-chunking" → "fix-amm-prices-chunking"
func SlugifyBranch(branch string) string {
	s := strings.ReplaceAll(branch, "/", "-")
	s = strings.Trim(s, "-")
	return s
}

// BranchPrefix returns "wt/<machine>/" for the given machine. Useful for
// filtering branches owned by this machine when deciding what to push.
func BranchPrefix(machine string) string {
	return "wt/" + machine + "/"
}
