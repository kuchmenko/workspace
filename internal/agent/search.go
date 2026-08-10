package agent

import (
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

const jumpLabels = "asdfghjklwertyuiopzxcvbnm"

type flashRefreshState struct {
	active, global, editing                 bool
	query, workspaceRoot, projectID, wtPath string
	returnSheet                             *sheet
}

func (m *Model) captureFlashRefresh() flashRefreshState {
	active := m.mode == viewFlash || m.mode == viewWhichKey && m.paletteOrigin != nil && m.paletteOrigin.mode == viewFlash
	state := flashRefreshState{active: active, global: m.flashGlobal, editing: m.flashEditing, query: m.flashQuery.Value(), returnSheet: m.flashReturnSheet}
	if item := m.currentItem(); item != nil {
		state.workspaceRoot = item.workspaceRoot
		if item.project != nil {
			state.projectID = item.project.ID
		}
		if item.worktree != nil {
			state.wtPath = item.path
		}
	}
	return state
}

func (m *Model) restoreFlashRefresh(state flashRefreshState) {
	if !state.active {
		return
	}
	paletteForeground := m.mode == viewWhichKey && m.paletteOrigin != nil && m.paletteOrigin.mode == viewFlash
	m.sheet = nil
	if state.global {
		m.openGlobalSearch()
		m.flashReturnSheet = m.reconcileLifecycleSheet(state.returnSheet)
	} else {
		m.mode = viewFlash
		m.flashGlobal = false
	}
	m.flashQuery.SetValue(state.query)
	m.flashEditing = state.editing
	m.recomputeFlash()
	m.restoreFlashSelection(state.workspaceRoot, state.projectID, state.wtPath)
	if state.editing {
		m.flashQuery.Focus()
	} else {
		m.flashQuery.Blur()
	}
	if paletteForeground {
		m.mode = viewWhichKey
	}
}

func (m *Model) updateFlash(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	if !m.flashEditing {
		return m.updateFlashResults(msg)
	}
	return m.updateFlashEditing(msg)
}

func (m *Model) updateFlashResults(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	matchCursor := 0
	for i, index := range m.flashMatches {
		if index == m.cursor {
			matchCursor = i
			break
		}
	}
	switch msg.String() {
	case "q", "esc":
		m.exitFlash(false)
	case "j", "down":
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[min(len(m.flashMatches)-1, matchCursor+1)]
		}
	case "k", "up":
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[max(0, matchCursor-1)]
		}
	case "enter":
		if len(m.flashMatches) > 0 {
			return m.activateFlashItem(m.cursor)
		}
	}
	return m, nil
}

func (m *Model) updateFlashEditing(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		m.exitFlash(false)
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
		m.flashEditing = false
		m.flashQuery.Blur()
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[0]
		}
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
		}
		var cmd tui.Cmd
		m.flashQuery, cmd = m.flashQuery.Update(msg)
		m.recomputeFlash()
		return m, cmd
	}
	return m, nil
}

func (m *Model) activateFlashItem(index int) (tui.Model, tui.Cmd) {
	item := m.items[index]
	if m.flashGlobal && item.kind == KindWorktree {
		project := item.parentProj
		m.exitFlash(true)
		m.sheet = newProjectSheet(m, project, nil)
		if !m.sheet.focusWorktreePath(item.path) {
			m.sheet = nil
			m.statusMsg = "target is no longer available"
		}
		return m, nil
	}
	m.cursor = index
	if m.flashGlobal && item.kind == KindProject {
		root, id := item.workspaceRoot, item.project.ID
		m.exitFlash(true)
		m.jumpToProject(root, id)
		m.sheet = newProjectSheet(m, item.project, nil)
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
			m.sheet = m.flashReturnSheet
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
	} else if !jumped {
		m.cursor, m.scroll = m.savedCursor, m.savedScroll
	}
	m.flashGlobal = false
	m.flashReturnSheet = nil
}

func (m *Model) openGlobalSearch() {
	m.savedItems, m.savedCursor, m.savedScroll = append([]listItem(nil), m.items...), m.cursor, m.scroll
	m.flashGlobal = true
	m.flashEditing = true
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
				wts := m.wtCache.Inventory(p.Path)
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
