package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// CreateWorktree creates a new worktree for `branch` in project p. The
// branch name is taken from the user verbatim — no prefix injection,
// no slug rewriting beyond what `git check-ref-format` accepts. If
// `branch` already exists locally in the bare repo, the new worktree
// attaches to it (the path that re-registers legacy wt/<machine>/*
// branches under the new schema). Otherwise it's created from the
// project's default branch.
//
// On success the workspace.toml registry is updated: this machine is
// claimed against the branch with ClaimBranch, last_active_* are set,
// and on first claim CreatedBy/CreatedAt are recorded.
//
// wsRoot and projID are required for the workspace.toml update; pass
// empty strings to skip persistence (best-effort fallback used by tests).
func CreateWorktree(p *Project, branch, wsRoot, projID string) (*WorktreeResult, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, fmt.Errorf("branch name required")
	}
	barePath := layout.BarePath(p.Path)
	if _, err := os.Stat(barePath); err != nil {
		return nil, fmt.Errorf("project not migrated (no bare repo at %s)", barePath)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	wtPath := layout.WorktreePathForBranch(p.Path, machine, branch)
	if _, err := os.Stat(wtPath); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", wtPath)
	}

	attachedToRemote := false
	if git.HasBranch(barePath, branch) {
		// Attach to existing local branch (covers re-registration of legacy
		// wt/<machine>/* checkouts and branches fetched from origin).
		if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
		// If the branch is also on origin, this attach amounts to "I just
		// observed it as published" — let the orphan detector treat it
		// as such.
		attachedToRemote = git.HasRemoteBranch(barePath, "origin", branch)
	} else {
		base := p.DefaultBranch
		if base == "" {
			base = "main"
		}
		if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
	}

	// Persist branch metadata. Best-effort: a failure here leaves the
	// worktree on disk; the user can re-run `ws worktree add` to fix the
	// registry, or the next reconciler tick will note the branch is
	// missing from [[branches]] and silently no-op (legacy-friendly path).
	if wsRoot != "" && projID != "" {
		if ws, err := config.Load(wsRoot); err == nil {
			if proj, ok := ws.Projects[projID]; ok {
				proj.ClaimBranch(branch, machine)
				if attachedToRemote {
					proj.MarkPushed(branch, machine, time.Now())
				}
				ws.Projects[projID] = proj
				if err := config.Save(wsRoot, ws); err != nil {
					return nil, fmt.Errorf("worktree created but workspace.toml save failed: %w", err)
				}
			}
		}
	}

	return &WorktreeResult{Path: wtPath, Branch: branch}, nil
}

// DeleteWorktree removes a worktree. Refuses if it's the main worktree.
// The workspace.toml [[branches]] entry is updated when wsRoot/projID
// are non-empty: this machine is released from the branch's machines
// slice; an empty machines slice causes the entry to be GC'd on Save.
func DeleteWorktree(mainPath, wtPath string, force bool) error {
	return DeleteWorktreeWithRegistry(mainPath, wtPath, force, "", "", "")
}

// DeleteWorktreeWithRegistry is the registry-aware variant. Pass empty
// strings for wsRoot/projID/branch to skip the workspace.toml update.
func DeleteWorktreeWithRegistry(mainPath, wtPath string, force bool, wsRoot, projID, branch string) error {
	if wtPath == mainPath {
		return fmt.Errorf("cannot delete main worktree")
	}
	barePath := layout.BarePath(mainPath)
	if err := git.WorktreeRemove(barePath, wtPath, force); err != nil {
		return err
	}
	if wsRoot == "" || projID == "" || branch == "" {
		return nil
	}
	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}
	ws, err := config.Load(wsRoot)
	if err != nil {
		return nil // best-effort
	}
	proj, ok := ws.Projects[projID]
	if !ok {
		return nil
	}
	if changed, _ := proj.ReleaseBranch(branch, machine); changed {
		ws.Projects[projID] = proj
		_ = config.Save(wsRoot, ws)
	}
	return nil
}

// worktreeDisplayName returns a human-readable short name for a worktree.
// For main it's "main". For wt/<machine>/<topic> (legacy) it extracts the
// topic. For everything else it shows the branch name (the new default).
func worktreeDisplayName(wt Worktree) string {
	if wt.IsMain {
		return "main"
	}
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	if wt.Branch != "" {
		return wt.Branch
	}
	return filepath.Base(wt.Path)
}

// WorktreeCache is a lazy, map-based cache for worktree listings.
// Worktrees are loaded from git on first access for a given project
// and served from memory on subsequent accesses. Invalidate after
// create/delete operations.
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
