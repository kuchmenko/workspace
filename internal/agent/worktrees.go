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

type Worktree struct {
	Path   string
	Branch string
	IsMain bool
	Dirty  bool
	Ahead  int
}

type WorktreeResult struct {
	Path   string
	Branch string
}

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

	if !git.HasFetchRefspec(barePath) {
		_ = git.SetFetchRefspec(barePath)
	}

	_ = git.FetchRefspec(barePath, "origin", branch)
	localExists := git.HasBranch(barePath, branch)
	remoteExists := git.HasRemoteBranch(barePath, "origin", branch)

	if existingPath := findWorktreeForBranch(barePath, branch); existingPath != "" {
		if wsRoot != "" && projID != "" {
			if ws, err := config.Load(wsRoot); err == nil {
				if proj, ok := ws.Projects[projID]; ok {
					proj.ClaimBranch(branch, machine)
					if remoteExists {
						proj.MarkPushed(branch, machine, time.Now())
					}
					ws.Projects[projID] = proj
					if err := config.Save(wsRoot, ws); err != nil {
						return nil, fmt.Errorf("registry update failed: %w", err)
					}
				}
			}
		}
		return &WorktreeResult{Path: existingPath, Branch: branch}, nil
	}

	wtPath := layout.WorktreePathForBranch(p.Path, machine, branch)
	if _, err := os.Stat(wtPath); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", wtPath)
	}

	attachedToRemote := false
	switch {
	case localExists:

		if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
		attachedToRemote = remoteExists
	case remoteExists:

		if err := git.WorktreeAdd(barePath, wtPath, branch, "origin/"+branch); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
		attachedToRemote = true
	default:
		base := p.DefaultBranch
		if base == "" {
			base = "main"
		}
		if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
	}

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
		return nil
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

func findWorktreeForBranch(barePath, branch string) string {
	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return ""
	}
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		if wt.Branch == branch {
			return wt.Path
		}
	}
	return ""
}

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

type WorktreeCache struct {
	data map[string][]Worktree
}

func NewWorktreeCache() *WorktreeCache {
	return &WorktreeCache{data: make(map[string][]Worktree)}
}

func (c *WorktreeCache) Get(mainPath string) []Worktree {
	if wts, ok := c.data[mainPath]; ok {
		return wts
	}
	wts := LoadWorktrees(mainPath)
	c.data[mainPath] = wts
	return wts
}

func (c *WorktreeCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

func LoadWorktrees(mainPath string) []Worktree {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
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
