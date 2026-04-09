package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// LoadWorkspaces returns all registered workspaces with their projects.
// Falls back to the workspace.toml under cwd if no daemon workspaces
// are registered.
func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, []string) {
	var diagnostics []string
	roots := workspaceRoots(fallbackRoot)
	if len(roots) == 0 {
		diagnostics = append(diagnostics, "no workspaces registered (run `ws daemon register` or cd into a workspace)")
		return nil, diagnostics
	}

	var result []WorkspaceData
	for _, root := range roots {
		ws, diags := loadOneWorkspace(root)
		diagnostics = append(diagnostics, diags...)
		if ws != nil {
			result = append(result, *ws)
		}
	}
	return result, diagnostics
}

func loadOneWorkspace(root string) (*WorkspaceData, []string) {
	var diagnostics []string
	w, err := config.Load(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", filepath.Base(root), err)}
	}

	ws := &WorkspaceData{
		Name: filepath.Base(root),
		Root: root,
	}

	// Collect groups.
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
	sort.Strings(names)
	for g := range groupSet {
		ws.Groups = append(ws.Groups, g)
	}
	sort.Strings(ws.Groups)

	// Collect projects.
	for _, name := range names {
		p := w.Projects[name]
		mainPath := filepath.Join(root, p.Path)
		proj := Project{
			ID:            name,
			Name:          name,
			Group:         p.Group,
			Category:      string(p.Category),
			Path:          mainPath,
			DefaultBranch: p.DefaultBranch,
		}

		// Count worktrees.
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

		// Count sessions.
		sessions := LoadSessions([]string{mainPath})
		proj.SessionCount = len(sessions)

		ws.Projects = append(ws.Projects, proj)
	}

	return ws, diagnostics
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
		}
	}

	sort.Strings(out)
	return out
}
