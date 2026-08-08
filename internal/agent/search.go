package agent

import (
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

const jumpLabels = "asdfghjklqwertyuiopzxcvbnm"

func (m *Model) updateFlash(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tui.Quit
	case "esc":
		m.exitFlash(false)
	case "backspace":
		if m.flashQuery.Value() != "" {
			m.flashQuery, _ = m.flashQuery.Update(msg)
			m.recomputeFlash()
		} else {
			m.exitFlash(false)
		}
	case "enter":
		if len(m.flashMatches) > 0 {
			return m.activateFlashItem(m.flashMatches[0])
		}
		m.exitFlash(true)
	default:
		if msg.Type == tui.KeyRunes {
			runes := msg.Runes
			if m.flashQuery.Value() != "" && len(runes) == 1 {
				ch := runes[0]
				for i, label := range m.flashLabels {
					if label != 0 && ch == label && i < len(m.flashMatches) {
						return m.activateFlashItem(m.flashMatches[i])
					}
				}
			}
			m.flashQuery, _ = m.flashQuery.Update(msg)
			m.recomputeFlash()
		}
	}
	return m, nil
}

func (m *Model) activateFlashItem(index int) (tui.Model, tui.Cmd) {
	item := m.items[index]
	if m.flashGlobal && item.kind == KindWorktree {
		return m.launch(item.workspaceRoot, item.path)
	}
	m.cursor = index
	if m.flashGlobal && item.kind == KindProject {
		root, id := item.workspaceRoot, item.project.ID
		m.exitFlash(true)
		m.jumpToProject(root, id)
		return m, nil
	}
	m.ensureVisible()
	m.exitFlash(true)
	return m, nil
}

func (m *Model) exitFlash(jumped bool) {
	m.flashQuery.Blur()
	m.mode = viewList
	if m.flashGlobal {
		if !jumped {
			m.items, m.cursor, m.scroll = m.savedItems, m.savedCursor, m.savedScroll
		} else {
			if m.savedExpanded != nil {
				m.expanded, m.savedExpanded = m.savedExpanded, nil
			}
			m.rebuildItems()
			m.ensureVisible()
		}
		m.savedItems = nil
		if m.savedExpanded != nil {
			m.expanded, m.savedExpanded = m.savedExpanded, nil
		}
	}
	m.flashGlobal = false
}

func (m *Model) openGlobalSearch() {
	m.savedItems, m.savedCursor, m.savedScroll = append([]listItem(nil), m.items...), m.cursor, m.scroll
	m.flashGlobal = true
	m.savedExpanded = make(map[string]bool, len(m.expanded))
	for k, v := range m.expanded {
		m.savedExpanded[k] = v
	}
	m.mode = viewFlash
	m.flashQuery.SetValue("")
	m.flashQuery.Focus()
	m.recomputeFlash()
}

func (m *Model) recomputeFlash() {
	query := strings.ToLower(m.flashQuery.Value())
	m.flashMatches = nil
	m.flashLabels = nil
	if m.flashGlobal {
		m.items = nil
		for wi := range m.workspaces {
			for pi := range m.workspaces[wi].Projects {
				p := &m.workspaces[wi].Projects[pi]
				projectName := p.Name
				if query == "" || strings.Contains(strings.ToLower(projectName), query) {
					m.items = append(m.items, listItem{kind: KindProject, workspaceRoot: p.WorkspaceRoot, project: p, path: p.Path})
				}
				wts, _ := m.wtCache.Get(p.Path)
				for i := range wts {
					name := p.Name + " › " + worktreeDisplayName(wts[i])
					if query == "" || strings.Contains(strings.ToLower(name), query) {
						wt := wts[i]
						m.items = append(m.items, listItem{kind: KindWorktree, workspaceRoot: p.WorkspaceRoot, group: name, worktree: &wt, parentProj: p, path: wt.Path})
					}
				}
			}
		}
		m.cursor, m.scroll = 0, 0
	}

	for i, item := range m.items {
		name := m.itemSearchName(item)
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			m.flashMatches = append(m.flashMatches, i)
		}
	}

	available := m.availableJumpLabels()
	for i := 0; i < len(m.flashMatches); i++ {
		if i < len(available) {
			m.flashLabels = append(m.flashLabels, available[i])
		} else {
			m.flashLabels = append(m.flashLabels, 0)
		}
	}
}

func (m *Model) availableJumpLabels() []rune {
	query := strings.ToLower(m.flashQuery.Value())
	if query == "" {
		return nil
	}
	var available []rune
	for _, r := range jumpLabels {
		extended := query + string(r)
		productive := false
		for _, item := range m.items {
			name := strings.ToLower(m.itemSearchName(item))
			if strings.Contains(name, extended) {
				productive = true
				break
			}
		}
		if !productive {
			available = append(available, r)
		}
	}
	return available
}

func (m *Model) itemSearchName(item listItem) string {
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group
	}
	return ""
}
