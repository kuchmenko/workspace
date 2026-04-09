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

// BuildGraph constructs the agent graph from real data sources:
//
//   1. The list of workspaces comes from ~/.config/ws/daemon.toml. If
//      that file is empty or absent, we fall back to the workspace.toml
//      that contains the current working directory (if any).
//   2. For each workspace, we load workspace.toml and iterate active
//      projects.
//   3. Projects with a `group` go under a synthetic group-node; the rest
//      hang directly under their workspace.
//   4. For each migrated project we list `git worktree list --porcelain`
//      and add one node per worktree (main + extras).
//   5. Action portals ([n]/[w]/[r]/[s]) hang under each project.
//
// Errors at any individual workspace/project level are recorded as a
// diagnostic node ("error: …") and the build continues — half a graph
// is more useful than none.
//
// Layer 4: orphan-session detection lives in layer 5 alongside the
// session parser; for now there is no orphan node.
func BuildGraph(fallbackRoot string) (*Graph, []string) {
	var diagnostics []string

	wsRoots := workspaceRoots(fallbackRoot)
	if len(wsRoots) == 0 {
		diagnostics = append(diagnostics, "no workspaces registered (run `ws daemon register` or cd into a workspace)")
		g := NewGraph(&Node{ID: "ws", Kind: KindRoot, Label: "ws"})
		return g, diagnostics
	}

	// Single-workspace case: skip the synthetic "ws" root entirely. The
	// only workspace becomes the graph's root, and its projects/groups
	// hang directly off it. Saves a navigation step on the common case.
	if len(wsRoots) == 1 {
		g, err := buildSingleWorkspace(wsRoots[0])
		if err != nil {
			diagnostics = append(diagnostics, err.Error())
		}
		return g, diagnostics
	}

	root := &Node{ID: "ws", Kind: KindRoot, Label: "ws"}
	g := NewGraph(root)
	for _, wr := range wsRoots {
		addWorkspace(g, wr, &diagnostics)
	}
	return g, diagnostics
}

// buildSingleWorkspace constructs a graph whose root IS the single
// registered workspace. Reuses addProject directly so the node IDs and
// payloads match the multi-workspace path.
func buildSingleWorkspace(root string) (*Graph, error) {
	wsName := filepath.Base(root)
	wsID := "ws/" + wsName
	g := NewGraph(&Node{
		ID:    wsID,
		Kind:  KindWorkspace,
		Label: wsName,
		Payload: map[string]string{
			"root": root,
		},
	})

	w, err := config.Load(root)
	if err != nil {
		return g, fmt.Errorf("%s: %v", wsName, err)
	}

	groupID := func(name string) string { return wsID + "/group:" + name }
	ensureGroup := func(name string) string {
		id := groupID(name)
		if _, ok := g.Nodes[id]; ok {
			return id
		}
		g.Add(wsID, &Node{ID: id, Kind: KindGroup, Label: name})
		return id
	}

	names := make([]string, 0, len(w.Projects))
	for n, p := range w.Projects {
		if p.Status == config.StatusArchived {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		p := w.Projects[name]
		parentID := wsID
		if p.Group != "" {
			parentID = ensureGroup(p.Group)
		}
		addProject(g, parentID, wsID, name, p, root)
	}
	return g, nil
}

// workspaceRoots returns the list of workspace root directories to render
// in the graph. Order is stable (alphabetical by path).
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

func addWorkspace(g *Graph, root string, diagnostics *[]string) {
	wsName := filepath.Base(root)
	wsID := "ws/" + wsName
	g.Add("ws", &Node{
		ID:    wsID,
		Kind:  KindWorkspace,
		Label: wsName,
		Payload: map[string]string{
			"root": root,
		},
	})

	w, err := config.Load(root)
	if err != nil {
		*diagnostics = append(*diagnostics, fmt.Sprintf("%s: %v", wsName, err))
		return
	}

	// Build group nodes lazily so empty groups don't pollute the graph.
	groupID := func(name string) string { return wsID + "/group:" + name }
	ensureGroup := func(name string) string {
		id := groupID(name)
		if _, ok := g.Nodes[id]; ok {
			return id
		}
		g.Add(wsID, &Node{
			ID:    id,
			Kind:  KindGroup,
			Label: name,
		})
		return id
	}

	// Iterate projects in stable order.
	names := make([]string, 0, len(w.Projects))
	for n, p := range w.Projects {
		if p.Status == config.StatusArchived {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		p := w.Projects[name]
		parentID := wsID
		if p.Group != "" {
			parentID = ensureGroup(p.Group)
		}
		addProject(g, parentID, wsID, name, p, root)
	}
}

func addProject(g *Graph, parentID, wsID, name string, p config.Project, wsRoot string) {
	projID := wsID + "/" + name
	mainPath := filepath.Join(wsRoot, p.Path)
	g.Add(parentID, &Node{
		ID:    projID,
		Kind:  KindProject,
		Label: name,
		Payload: map[string]string{
			"name":     name,
			"path":     mainPath,
			"category": string(p.Category),
		},
	})

	// Worktrees are NOT added as graph nodes — they would steal cardinal
	// slots from sibling projects and clutter the canvas. They'll be
	// surfaced in a floating panel when the user focuses on a project
	// node (layer 6+). Worktree data is still accessible via project
	// Payload for the launcher.

	// Store worktree count in payload for badge rendering.
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err == nil {
		if wts, err := git.WorktreeList(barePath); err == nil {
			count := 0
			for _, wt := range wts {
				if !wt.Bare {
					count++
				}
			}
			if node := g.Nodes[projID]; node != nil {
				if node.Payload == nil {
					node.Payload = map[string]string{}
				}
				node.Payload["worktree_count"] = fmt.Sprintf("%d", count)
			}
		}
	}
}
