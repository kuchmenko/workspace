package agent

import "github.com/kuchmenko/workspace/internal/config"

func (m *Model) toggleExpand(key string) {
	m.expanded[key] = !m.expanded[key]
	m.rebuildItems()
	m.ensureVisible()
}

func (m *Model) jumpToGroup(workspaceRoot, group string) {
	for i, it := range m.items {
		if it.kind == KindGroup && it.workspaceRoot == workspaceRoot && it.group == group {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

func (m *Model) jumpToExpandKey(key string) {
	for i, it := range m.items {
		if it.kind == KindGroup && it.expandKey == key {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

func (m *Model) jumpToProject(workspaceRoot, projID string) {
	for _, ws := range m.workspaces {
		if ws.Root != workspaceRoot {
			continue
		}
		for _, project := range ws.Projects {
			if project.ID != projID {
				continue
			}
			switch m.homeView {
			case config.ExplorerViewProjects:
				if project.Group != "" {
					m.expanded[groupKey(workspaceRoot, project.Group)] = true
				}
			case config.ExplorerViewLanguage:
				m.expanded[languageKey(project.Language)] = true
			case config.ExplorerViewRecent:
				m.expanded[recentKey()] = true
			}
			m.rebuildItems()
			break
		}
	}
	for i, it := range m.items {
		if it.kind == KindProject && it.workspaceRoot == workspaceRoot && it.project != nil && it.project.ID == projID {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}
