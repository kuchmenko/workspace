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

func languageKey(language string) string { return "language\x00" + language }

func recentKey() string { return "projection\x00recent" }

func (m *Model) rebuildItems() {
	m.items = nil
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			m.refreshProjectRecency(&m.workspaces[wi].Projects[pi])
		}
	}
	m.headerChips = buildHeaderChips(m.workspaces)
	switch m.homeView {
	case config.ExplorerViewRecent:
		m.rebuildRecentItems()
	case config.ExplorerViewLanguage:
		m.rebuildLanguageItems()
	default:
		m.rebuildProjectItems()
	}
	m.clampCursor()
}

func (m *Model) rebuildRecentItems() {
	key := recentKey()
	if _, ok := m.expanded[key]; !ok {
		m.expanded[key] = true
	}
	m.items = append(m.items, listItem{kind: KindGroup, group: "Recent", expandKey: key, projectionGroup: true})
	if !m.expanded[key] {
		return
	}
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			m.addProjectItem(&m.workspaces[wi].Projects[pi], 1)
			m.items[len(m.items)-1].expandKey = key
		}
	}
	projects := m.items[1:]
	sort.SliceStable(projects, func(i, j int) bool {
		return recencyLess(projects[i].project.LastActiveAt, projects[j].project.LastActiveAt, projects[i].project.Name, projects[j].project.Name, m.recentOrder == config.RecentOrderDesc)
	})
}

func (m *Model) rebuildLanguageItems() {
	languages := map[string][]*Project{}
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			p := &m.workspaces[wi].Projects[pi]
			languages[p.Language] = append(languages[p.Language], p)
		}
	}
	names := make([]string, 0, len(languages))
	for name := range languages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.addLanguageItems(name, languages[name])
	}
}

func (m *Model) addLanguageItems(name string, projects []*Project) {
	key := languageKey(name)
	m.items = append(m.items, listItem{kind: KindGroup, group: name, expandKey: key, projectionGroup: true})
	if !m.expanded[key] {
		return
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	for _, p := range projects {
		m.addProjectItem(p, 1)
		m.items[len(m.items)-1].expandKey = key
	}
}

func (m *Model) rebuildProjectItems() {
	for _, ws := range m.workspaces {
		projects := make([]*Project, 0, len(ws.Projects))
		groupActivity := make(map[string]time.Time, len(ws.Groups))
		for i := range ws.Projects {
			p := &ws.Projects[i]
			if p.WorkspaceRoot == "" {
				p.WorkspaceRoot = ws.Root
			}
			projects = append(projects, p)
			if p.LastActiveAt.After(groupActivity[p.Group]) {
				groupActivity[p.Group] = p.LastActiveAt
			}
		}
		sort.SliceStable(projects, func(i, j int) bool {
			if !projects[i].LastActiveAt.Equal(projects[j].LastActiveAt) {
				return projects[i].LastActiveAt.After(projects[j].LastActiveAt)
			}
			return projects[i].Name < projects[j].Name
		})

		for _, p := range projects {
			if p.Group == "" {
				m.addProjectItem(p, 0)
			}
		}

		groups := append([]string(nil), ws.Groups...)
		sort.SliceStable(groups, func(i, j int) bool {
			if !groupActivity[groups[i]].Equal(groupActivity[groups[j]]) {
				return groupActivity[groups[i]].After(groupActivity[groups[j]])
			}
			return groups[i] < groups[j]
		})
		for _, g := range groups {
			key := groupKey(ws.Root, g)
			groupPath := filepath.Join(ws.Root, g)
			m.items = append(m.items, listItem{kind: KindGroup, workspaceRoot: ws.Root, group: g, indent: 0, path: groupPath, expandKey: key})
			if m.expanded[key] {
				for _, p := range projects {
					if p.Group == g {
						m.addProjectItem(p, 1)
					}
				}
			}
		}
	}
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
	wts, err := m.wtCache.Get(p.Path)
	if err != nil {
		return
	}
	p.WorktreeCount = len(wts)
	var latest time.Time
	for i := range wts {
		if active := p.BranchActivity[wts[i].Branch]; active.After(wts[i].LastActiveAt) {
			wts[i].LastActiveAt = active
		}
		if wts[i].LastActiveAt.After(latest) {
			latest = wts[i].LastActiveAt
		}
	}
	for _, active := range p.BranchActivity {
		if active.After(latest) {
			latest = active
		}
	}
	p.LastActiveAt = latest
	m.wtCache.data[p.Path] = wts
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

func (m *Model) addProjectItem(p *Project, indent int) {
	key := ""
	if p.Group != "" {
		key = groupKey(p.WorkspaceRoot, p.Group)
	}
	m.items = append(m.items, listItem{kind: KindProject, workspaceRoot: p.WorkspaceRoot, project: p, indent: indent, path: p.Path, expandKey: key})
}
