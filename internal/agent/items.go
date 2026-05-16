package agent

// rebuildItems flattens the workspace tree into the scrollable item
// list. The pinned quick-nav header is rendered separately (see
// renderHeaderChips) and never enters m.items — keeping the header
// fixed above the scroll viewport requires it to be outside the
// scrollable region.
func (m *Model) rebuildItems() {
	m.items = nil
	m.headerProjects = headerProjects(allProjects(m.workspaces))

	for _, ws := range m.workspaces {
		// Ungrouped projects first.
		for i := range ws.Projects {
			p := &ws.Projects[i]
			if p.Group == "" {
				m.addProjectItem(p, 0)
			}
		}
		// Then groups.
		for _, g := range ws.Groups {
			m.items = append(m.items, listItem{kind: KindGroup, group: g, indent: 0, path: GroupPath(ws.Root, g)})
			if m.expanded[g] {
				for i := range ws.Projects {
					p := &ws.Projects[i]
					if p.Group == g {
						m.addProjectItem(p, 1)
					}
				}
			}
		}
	}
	m.clampCursor()
}

// clampCursor keeps m.cursor inside the items range. Every row in
// m.items is selectable now that section headers live outside the
// scroll list, but we still bracket-clamp the index for safety after
// rebuilds that may have shrunk the list.
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
	m.items = append(m.items, listItem{kind: KindProject, project: p, indent: indent, path: p.Path})

	// If project is expanded (tab), show worktrees + sessions inline.
	if !m.expanded["proj:"+p.ID] {
		return
	}

	wts := m.wtCache.Get(p.Path)
	for i := range wts {
		wt := &wts[i]
		name := worktreeDisplayName(*wt)
		m.items = append(m.items, listItem{
			kind:       KindWorktree,
			worktree:   wt,
			indent:     indent + 1,
			path:       wt.Path,
			parentProj: p,
			group:      name,
		})
	}

	sessions := m.sessCache.Get(p.Path)
	if len(sessions) > 5 {
		sessions = sessions[:5]
	}
	for i := range sessions {
		s := &sessions[i]
		m.items = append(m.items, listItem{
			kind:       KindPortal,
			session:    s,
			indent:     indent + 1,
			path:       s.Cwd,
			parentProj: p,
		})
	}
}
