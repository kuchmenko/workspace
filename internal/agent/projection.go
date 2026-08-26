package agent

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

func groupKey(wsRoot, group string) string {
	return wsRoot + "\x00" + group
}

func workspaceKey(root string) string { return "workspace\x00" + root }

func recentKey() string { return "projection\x00recent" }

func (m *Model) rebuildItems() {
	m.items = nil
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			m.refreshProjectRecency(&m.workspaces[wi].Projects[pi])
		}
	}
	switch m.homeView {
	case config.ExplorerViewRecent:
		m.rebuildRecentItems()
	default:
		m.rebuildProjectItems()
	}
	m.clampCursor()
}

func (m *Model) rebuildRecentItems() {
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			m.addProjectItem(&m.workspaces[wi].Projects[pi], 0, "")
		}
	}
	sort.SliceStable(m.items, func(i, j int) bool {
		return recencyLess(m.items[i].project.LastActiveAt, m.items[j].project.LastActiveAt, m.items[i].project.Name, m.items[j].project.Name, m.recentOrder == config.RecentOrderDesc)
	})
}

func (m *Model) rebuildProjectItems() {
	if m.expanded == nil {
		m.expanded = make(map[string]bool)
	}
	showWorkspaces := len(m.workspaces) > 1
	for _, ws := range m.workspaces {
		projects := make([]*Project, 0, len(ws.Projects))
		groupCounts := make(map[string]int, len(ws.Groups))
		for i := range ws.Projects {
			p := &ws.Projects[i]
			if p.WorkspaceRoot == "" {
				p.WorkspaceRoot = ws.Root
			}
			projects = append(projects, p)
			groupCounts[p.Group]++
		}
		sort.SliceStable(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })

		rootIndent, rootExpandKey, visible := m.addProjectWorkspace(ws, len(projects), showWorkspaces)
		if !visible {
			continue
		}

		for _, p := range projects {
			if p.Group == "" {
				m.addProjectItem(p, rootIndent, rootExpandKey)
			}
		}

		groups := append([]string(nil), ws.Groups...)
		sort.Strings(groups)
		for _, g := range groups {
			key := groupKey(ws.Root, g)
			groupPath := filepath.Join(ws.Root, g)
			m.items = append(m.items, listItem{kind: KindGroup, workspaceRoot: ws.Root, group: g, indent: rootIndent, count: groupCounts[g], path: groupPath, expandKey: key, parentExpandKey: rootExpandKey})
			if m.expanded[key] {
				for _, p := range projects {
					if p.Group == g {
						m.addProjectItem(p, rootIndent+1, key)
					}
				}
			}
		}
	}
}

func (m *Model) addProjectWorkspace(workspace WorkspaceData, count int, show bool) (int, string, bool) {
	key := workspaceKey(workspace.Root)
	if _, exists := m.expanded[key]; !exists {
		m.expanded[key] = true
	}
	if !show {
		return 0, "", true
	}
	m.items = append(m.items, listItem{kind: KindWorkspace, workspaceRoot: workspace.Root, workspaceName: workspace.Name, count: count, path: workspace.Root, expandKey: key})
	return 1, key, m.expanded[key]
}

func recencyLess(a, b time.Time, an, bn string, desc bool) bool {
	if a.Equal(b) {
		return an < bn
	}
	if a.IsZero() {
		return false
	}
	if b.IsZero() {
		return true
	}
	if desc {
		return a.After(b)
	}
	return a.Before(b)
}

func (m *Model) refreshProjectRecency(p *Project) {
	if m.wtCache == nil {
		return
	}
	wts := m.wtCache.Inventory(p.Path)
	p.WorktreeCount = len(wts)
	var latest time.Time
	for _, active := range p.BranchActivity {
		if active.After(latest) {
			latest = active
		}
	}
	p.LastActiveAt = projectRecency(latest, wts)
}

func (m *Model) clampCursor() {
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) addProjectItem(p *Project, indent int, parentExpandKey string) {
	m.items = append(m.items, listItem{kind: KindProject, workspaceRoot: p.WorkspaceRoot, project: p, indent: indent, path: p.Path, expandKey: parentExpandKey})
}
