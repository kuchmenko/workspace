// Package layout computes the canonical filesystem paths for the
// worktree-based project layout. Centralized so that migrate, sync,
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

func BarePath(mainWorktree string) string {
	return mainWorktree + ".bare"
}

func WorktreeDirName(projectBaseName, machine, topic string) string {
	safeTopic := strings.ReplaceAll(topic, "/", "-")
	return projectBaseName + "-wt-" + machine + "-" + safeTopic
}

func WorktreePath(mainWorktree, machine, topic string) string {
	dir := filepath.Dir(mainWorktree)
	base := filepath.Base(mainWorktree)
	return filepath.Join(dir, WorktreeDirName(base, machine, topic))
}

func WorktreePathForBranch(mainWorktree, machine, branch string) string {
	dir := filepath.Dir(mainWorktree)
	base := filepath.Base(mainWorktree)
	name := WorktreeDirName(base, machine, branch)
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); errors.Is(err, fs.ErrNotExist) {
		return candidate
	}
	sum := sha1.Sum([]byte(branch))
	suffix := hex.EncodeToString(sum[:4])
	return filepath.Join(dir, name+"-"+suffix)
}

func BranchName(machine, topic string) string {
	return "wt/" + machine + "/" + topic
}

func SlugifyBranch(branch string) string {
	s := strings.ReplaceAll(branch, "/", "-")
	s = strings.Trim(s, "-")
	return s
}
