package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, *SessionCache, []string) {
	var diagnostics []string
	roots := workspaceRoots(fallbackRoot)
	if len(roots) == 0 {
		diagnostics = append(diagnostics, "no workspaces registered (run `ws daemon register` or cd into a workspace)")
		return nil, nil, diagnostics
	}

	cache := NewSessionCache()
	var result []WorkspaceData
	for _, root := range roots {
		ws, diags := loadOneWorkspace(root, cache)
		diagnostics = append(diagnostics, diags...)
		if ws != nil {
			result = append(result, *ws)
		}
	}
	return result, cache, diagnostics
}

func loadOneWorkspace(root string, sessCache *SessionCache) (*WorkspaceData, []string) {
	var diagnostics []string
	w, err := config.Load(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", filepath.Base(root), err)}
	}

	ws := &WorkspaceData{
		Name:           filepath.Base(root),
		Root:           root,
		FavoriteGroups: map[string]bool{},
	}

	groupSet := map[string]bool{}
	names := make([]string, 0, len(w.Projects))
	for n, p := range w.Projects {
		if p.Status == config.StatusArchived {
			continue
		}
		names = append(names, n)
		if p.Group != "" {
			groupSet[p.Group] = true
		}
	}
	for g := range w.Groups {
		groupSet[g] = true
	}
	sort.Strings(names)
	for g := range groupSet {
		ws.Groups = append(ws.Groups, g)
		if entry, ok := w.Groups[g]; ok && entry.Favorite {
			ws.FavoriteGroups[g] = true
		}
	}
	sort.Strings(ws.Groups)

	for _, name := range names {
		p := w.Projects[name]
		mainPath := filepath.Join(root, p.Path)
		lastAt, lastMachine := projectActivity(p.Branches)
		proj := Project{
			ID:                name,
			Name:              name,
			Group:             p.Group,
			Category:          string(p.Category),
			Path:              mainPath,
			DefaultBranch:     p.DefaultBranch,
			Favorite:          p.Favorite,
			LastActiveAt:      lastAt,
			LastActiveMachine: lastMachine,
		}

		barePath := layout.BarePath(mainPath)
		if _, err := os.Stat(barePath); err == nil {
			if wts, err := git.WorktreeList(barePath); err == nil {
				count := 0
				for _, wt := range wts {
					if !wt.Bare {
						count++
					}
				}
				proj.WorktreeCount = count
			}
		}

		proj.SessionCount = sessCache.Count(mainPath)

		ws.Projects = append(ws.Projects, proj)
	}

	return ws, diagnostics
}

func projectActivity(branches []config.BranchMeta) (time.Time, string) {
	var best time.Time
	var machine string
	for _, b := range branches {
		if b.LastActiveAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, b.LastActiveAt)
		if err != nil {
			continue
		}
		if t.After(best) {
			best = t
			machine = b.LastActiveMachine
		}
	}
	return best, machine
}

func workspaceRoots(fallback string) []string {
	seen := map[string]bool{}
	var out []string

	cfg, err := daemon.LoadConfig()
	if err == nil && cfg != nil {
		for _, w := range cfg.Workspaces {
			if w.Root == "" || seen[w.Root] {
				continue
			}
			if _, err := os.Stat(filepath.Join(w.Root, "workspace.toml")); err != nil {
				continue
			}
			seen[w.Root] = true
			out = append(out, w.Root)
		}
	}

	if len(out) == 0 && fallback != "" {
		if _, err := os.Stat(filepath.Join(fallback, "workspace.toml")); err == nil {
			out = append(out, fallback)
		} else if root, err := config.FindRoot(); err == nil && !seen[root] {
			out = append(out, root)
		}
	}

	sort.Strings(out)
	return out
}
