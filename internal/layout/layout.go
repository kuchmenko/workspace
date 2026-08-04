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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func ProjectPath(workspaceRoot, projectPath string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", errors.New("project path must not be empty")
	}
	if filepath.IsAbs(projectPath) {
		return "", fmt.Errorf("project path %q must be relative", projectPath)
	}
	clean := filepath.Clean(projectPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project path %q must stay inside the workspace", projectPath)
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	target := filepath.Join(root, clean)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	ancestor := target
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect project path %q: %w", projectPath, err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("project path %q has no existing workspace ancestor", projectPath)
		}
		ancestor = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", projectPath, err)
	}
	inside, err := filepath.Rel(resolvedRoot, resolvedAncestor)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("project path %q escapes the workspace through a symlink", projectPath)
	}
	return target, nil
}

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
