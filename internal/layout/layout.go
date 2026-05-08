// Package layout computes the canonical filesystem paths for the
// worktree-based project layout. Centralized so that migrate, reconciler,
// archive, restore, scan and clean all agree on where things live.
//
// For a project with workspace-relative path "personal/myapp" the layout is:
//
//	<wsRoot>/personal/myapp           ← main worktree (default branch)
//	<wsRoot>/personal/myapp.bare      ← bare repo, source of truth
//	<wsRoot>/personal/myapp-wt-<machine>-<branch-slug>  ← extra worktrees
//
// All helpers in this package operate on absolute paths. Callers are expected
// to have already joined the workspace root with the project's relative path.
package layout

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
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

// WorktreePath returns the absolute path of an extra worktree given the
// main worktree path and the worktree's machine + topic. This is the
// raw-name variant; for branches that may slug-collide with another
// branch already present in the parent dir, use WorktreePathForBranch
// which picks a deterministic suffix.
func WorktreePath(mainWorktree, machine, topic string) string {
	dir := filepath.Dir(mainWorktree)
	base := filepath.Base(mainWorktree)
	return filepath.Join(dir, WorktreeDirName(base, machine, topic))
}

// WorktreePathForBranch returns the absolute worktree path for `branch`
// on `machine`, choosing a directory name that does not collide with
// any pre-existing entry in the parent of `mainWorktree`.
//
// Two distinct branches whose slugs collide (`feat/foo-bar` and
// `feat/foo/bar` both flatten to `feat-foo-bar`) would otherwise share
// the same directory name. The resolution is to append `-<sha8>` from
// SHA-1(branch) when the unsuffixed candidate already exists. The hash
// is deterministic per branch, so two machines independently adding
// the same branch land on the same path even when each machine sees a
// different "first claimant" in its local filesystem.
//
// The first claimant on a given machine gets the unsuffixed path; every
// subsequent slug-colliding branch on the same machine receives the
// hash suffix. Order of arrival across machines is not coordinated, so
// two machines that add the same pair of slug-colliding branches in
// opposite orders will end up with mirror-image directory names — the
// cross-machine guarantee is per-branch identity, not per-pair-order.
func WorktreePathForBranch(mainWorktree, machine, branch string) string {
	dir := filepath.Dir(mainWorktree)
	base := filepath.Base(mainWorktree)
	name := WorktreeDirName(base, machine, branch)
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); errors.Is(err, fs.ErrNotExist) {
		return candidate
	}
	sum := sha1.Sum([]byte(branch))
	suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
	return filepath.Join(dir, name+"-"+suffix)
}

// BranchName builds the canonical wt/<machine>/<topic> branch name.
// Used by `ws migrate` for migration-internal WIP/stash/detached branches
// that go straight into the bare repo and never face the daemon's push
// path. New worktrees no longer use this naming scheme — `ws worktree
// add` accepts a literal branch name from the user.
func BranchName(machine, topic string) string {
	return "wt/" + machine + "/" + topic
}

// SlugifyBranch converts a branch name to a filesystem-safe directory
// component: slashes → dashes, strip leading/trailing dashes.
//
//	"feat/buddy" → "feat-buddy"
//	"fix/amm-prices-chunking" → "fix-amm-prices-chunking"
func SlugifyBranch(branch string) string {
	s := strings.ReplaceAll(branch, "/", "-")
	s = strings.Trim(s, "-")
	return s
}
