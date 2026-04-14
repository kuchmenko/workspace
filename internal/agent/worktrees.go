package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// Worktree is a single worktree of a project, loaded on demand.
type Worktree struct {
	Path   string
	Branch string
	IsMain bool
	Dirty  bool // has uncommitted changes
	Ahead  int  // commits ahead of upstream (0 if no upstream)
}

// WorktreeResult is returned after successful worktree creation.
type WorktreeResult struct {
	Path   string
	Branch string
}

// CreateWorktree creates a new worktree for the given project. Mirrors
// the logic of `ws worktree new` but callable from the TUI.
//
// topic:        required — the worktree topic name (e.g. "fix-login")
// customBranch: optional — overrides the default wt/<machine>/<topic>
// autoPush:     whether to add the branch to daemon auto-push
// CreateWorktree creates a new worktree for the given project.
//
// When customBranch is set, the topic is IGNORED — the worktree directory
// name is derived from the slugified branch name instead:
//
//	branch "feat/buddy" → dir "myapp-wt-linux-feat-buddy"
//	branch "" + topic "buddy" → dir "myapp-wt-linux-buddy"  (default wt/<machine>/buddy)
//
// This matches the convention in CLAUDE.md: worktree dirs flatten
// slashes to dashes, and the branch name is the source of truth when
// explicitly provided.
func CreateWorktree(p *Project, topic, customBranch string, autoPush bool) (*WorktreeResult, error) {
	barePath := layout.BarePath(p.Path)
	if _, err := os.Stat(barePath); err != nil {
		return nil, fmt.Errorf("project not migrated (no bare repo at %s)", barePath)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	var branch string
	var pathTopic string // what goes into the worktree directory name
	if customBranch != "" {
		branch = customBranch
		// Derive path topic from branch: feat/buddy → feat-buddy
		pathTopic = layout.SlugifyBranch(customBranch)
	} else {
		branch = layout.BranchName(machine, topic)
		pathTopic = topic
	}

	wtPath := layout.WorktreePath(p.Path, machine, pathTopic)
	if _, err := os.Stat(wtPath); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", wtPath)
	}
	if git.HasBranch(barePath, branch) {
		return nil, fmt.Errorf("branch %s already exists", branch)
	}

	base := p.DefaultBranch
	if base == "" {
		base = "main"
	}

	if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}

	_ = autoPush
	return &WorktreeResult{Path: wtPath, Branch: branch}, nil
}


// DeleteWorktree removes a worktree. Refuses if it's the main worktree.
func DeleteWorktree(mainPath, wtPath string, force bool) error {
	if wtPath == mainPath {
		return fmt.Errorf("cannot delete main worktree")
	}
	barePath := layout.BarePath(mainPath)
	return git.WorktreeRemove(barePath, wtPath, force)
}

// PromoteWorktree renames a wt/<machine>/<topic> branch to a
// repository-native branch name (e.g. feat/fix-login) AND moves the
// worktree directory to match, so the on-disk name reflects the new
// branch.
//
// Steps (matching the CLI `ws worktree promote` flow):
//  1. Move worktree directory: old-path → new-path (git worktree move)
//  2. Rename branch: old-branch → new-branch (git branch -m)
//  3. If branch rename fails, roll back the directory move.
//  4. Update autopush in workspace.toml (release old, claim new).
//  5. Delete old remote ref, push new branch (best-effort).
//
// wsRoot and projID are needed for step 4 (workspace.toml update).
// Pass empty strings to skip autopush update.
func PromoteWorktree(mainPath string, wt Worktree, newBranch, wsRoot, projID string) error {
	if wt.IsMain {
		return fmt.Errorf("cannot promote main worktree")
	}
	if newBranch == "" {
		return fmt.Errorf("new branch name required")
	}

	barePath := layout.BarePath(mainPath)
	if !git.HasBranch(barePath, wt.Branch) {
		return fmt.Errorf("branch %s does not exist", wt.Branch)
	}
	if git.HasBranch(barePath, newBranch) {
		return fmt.Errorf("branch %s already exists", newBranch)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	// New directory path derived from slugified new branch name.
	newPath := filepath.Join(
		filepath.Dir(mainPath),
		layout.WorktreeDirName(filepath.Base(mainPath), machine, layout.SlugifyBranch(newBranch)),
	)
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("target path already exists: %s", newPath)
	}

	// Step 1: move worktree directory.
	if err := git.WorktreeMove(barePath, wt.Path, newPath); err != nil {
		return fmt.Errorf("move worktree: %w", err)
	}

	// Step 2: rename branch inside the new worktree path (same as CLI
	// promote). On failure, roll back the move.
	if err := git.BranchRename(newPath, wt.Branch, newBranch); err != nil {
		// Roll back directory move.
		_ = git.WorktreeMove(barePath, newPath, wt.Path)
		return fmt.Errorf("rename branch: %w", err)
	}

	// Step 3: best-effort delete the old remote ref.
	_ = git.DeleteRemoteBranch(barePath, wt.Branch)

	// Step 4: update autopush in workspace.toml.
	if wsRoot != "" && projID != "" {
		if ws, err := config.Load(wsRoot); err == nil {
			if proj, ok := ws.Projects[projID]; ok {
				proj.ReleaseAutopushBranch(wt.Branch)
				proj.ClaimAutopushBranch(newBranch, machine, false)
				ws.Projects[projID] = proj
				_ = config.Save(wsRoot, ws)
			}
		}
	}

	// Step 5: best-effort push the renamed branch.
	_ = git.PushBranch(barePath, newBranch)

	return nil
}

// worktreeDisplayName returns a human-readable short name for a worktree.
// For main it's "main". For wt/<machine>/<topic> it extracts the topic.
// For custom branches it shows the branch. For long directory-derived
// names it extracts the meaningful suffix.
func worktreeDisplayName(wt Worktree) string {
	if wt.IsMain {
		return "main"
	}
	// Try to extract topic from wt/<machine>/<topic> branch name.
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return parts[2] // topic
		}
	}
	// Custom branch — show as-is.
	if wt.Branch != "" {
		return wt.Branch
	}
	// Fallback to directory base name.
	return filepath.Base(wt.Path)
}

// WorktreeCache is a lazy, map-based cache for worktree listings.
// Worktrees are loaded from git on first access for a given project
// and served from memory on subsequent accesses. Invalidate after
// create/delete/promote operations.
type WorktreeCache struct {
	data map[string][]Worktree // mainPath → worktrees
}

// NewWorktreeCache creates an empty worktree cache.
func NewWorktreeCache() *WorktreeCache {
	return &WorktreeCache{data: make(map[string][]Worktree)}
}

// Get returns worktrees for the given mainPath, loading from git on
// first access and caching the result.
func (c *WorktreeCache) Get(mainPath string) []Worktree {
	if wts, ok := c.data[mainPath]; ok {
		return wts
	}
	wts := LoadWorktrees(mainPath)
	c.data[mainPath] = wts
	return wts
}

// Invalidate removes cached worktrees for a path, forcing a reload
// on the next Get call.
func (c *WorktreeCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

// LoadWorktrees returns the worktrees for a project. Requires the
// project to be migrated (bare repo exists). Populates Dirty and
// Ahead fields by querying git status for each worktree.
func LoadWorktrees(mainPath string) []Worktree {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		// Not migrated — return just the main path.
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	var result []Worktree
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		w := Worktree{
			Path:   wt.Path,
			Branch: wt.Branch,
			IsMain: wt.Path == mainPath,
			Dirty:  git.IsDirty(wt.Path),
		}
		ahead, _, _ := git.AheadBehind(wt.Path, wt.Branch)
		w.Ahead = ahead
		result = append(result, w)
	}
	return result
}
