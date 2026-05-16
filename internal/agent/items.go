package agent

import "github.com/kuchmenko/workspace/internal/config"

// rebuildItems flattens the workspace tree into a visible list,
// respecting group expansion state and the active agent view.
//
// Layout depends on m.agentView:
//
//   - "favorites": only the Favorites header section is shown.
//     Empty favorites produce a hint row pointing the user at the
//     `f` hotkey. The full workspace tree is intentionally hidden.
//
//   - "all" (default): if there are any favorites or any recent
//     non-favorite activity, emit a Favorites section then a
//     Recent section then a `-- all workspaces --` divider above
//     the regular tree. With no activity at all, the header is
//     skipped entirely and the user sees just the tree.
func (m *Model) rebuildItems() {
	m.items = nil

	favs, recent := headerSections(allProjects(m.workspaces))

	if m.agentView == config.AgentViewFavorites {
		m.appendSectionTitle("Favorites")
		if len(favs) == 0 {
			m.appendSectionHint("(no favorites yet — press f on a project)")
		} else {
			for i := range favs {
				m.appendHeaderProject(&favs[i])
			}
		}
		m.clampCursor()
		return
	}

	headerShown := false
	if len(favs) > 0 {
		m.appendSectionTitle("Favorites")
		for i := range favs {
			m.appendHeaderProject(&favs[i])
		}
		headerShown = true
	}
	if len(recent) > 0 {
		m.appendSectionTitle("Recent")
		for i := range recent {
			m.appendHeaderProject(&recent[i])
		}
		headerShown = true
	}
	if headerShown {
		m.appendSectionDivider("-- all workspaces --")
	}

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

// appendSectionTitle pushes a non-selectable header row carrying the
// label rendered above each shortcut section ("Favorites", "Recent").
func (m *Model) appendSectionTitle(title string) {
	m.items = append(m.items, listItem{kind: KindSection, sectionTitle: title})
}

// appendSectionHint pushes a non-selectable hint row used inside an
// empty Favorites view to point the user at the `f` hotkey. Visually
// distinct from a title via the leading "(" — the renderer uses the
// same style block for both.
func (m *Model) appendSectionHint(text string) {
	m.items = append(m.items, listItem{kind: KindSection, sectionTitle: text})
}

// appendSectionDivider pushes a non-selectable divider line drawn
// between the shortcut header and the full tree.
func (m *Model) appendSectionDivider(text string) {
	m.items = append(m.items, listItem{kind: KindSection, sectionTitle: text})
}

// appendHeaderProject emits a project row inside the Favorites/Recent
// shortcut section. The row is fully selectable and launches just
// like a tree-row project on Enter, but inHeader=true suppresses the
// worktree/session expansion children — these are quick-nav rows,
// not a place for nested navigation.
func (m *Model) appendHeaderProject(p *Project) {
	m.items = append(m.items, listItem{
		kind:     KindProject,
		project:  p,
		indent:   0,
		path:     p.Path,
		inHeader: true,
	})
}

// clampCursor keeps m.cursor inside the items range and pulls it off
// any KindSection rows it might have landed on after a rebuild. When
// the cursor is on a non-selectable row, prefer moving downward first
// (the natural reading direction) and only fall back to upward if
// nothing selectable lies below.
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
	if m.items[m.cursor].isSelectable() {
		return
	}
	if next := m.nextSelectable(m.cursor, +1); next != m.cursor && m.items[next].isSelectable() {
		m.cursor = next
		return
	}
	if next := m.nextSelectable(m.cursor, -1); next != m.cursor && m.items[next].isSelectable() {
		m.cursor = next
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
