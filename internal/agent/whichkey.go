package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/config"
)

type whichKeyAction struct {
	key  string
	desc string
}

func (m *Model) whichKeyActions() []whichKeyAction {
	item := m.currentItem()
	if item == nil {
		return nil
	}

	if m.whichKeyLevel == 1 {
		// Worktree sub-menu.
		return []whichKeyAction{
			{"n", "new worktree"},
			{"", ""},
			{"esc", "back"},
		}
	}

	switch item.kind {
	case KindGroup:
		return []whichKeyAction{
			{"⏎", "open claude"},
			{"p", "+prompt"},
			{"f", m.favoriteToggleLabelGroup(item.group)},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"⏎", "open claude"},
			{"p", "+prompt"},
			{"f", m.favoriteToggleLabel(item)},
			{"w", "worktree ›"},
			{"e", "edit"},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindWorktree:
		actions := []whichKeyAction{
			{"⏎", "open claude"},
			{"p", "+prompt"},
			{"l", "shell"},
		}
		if item.worktree != nil && !item.worktree.IsMain {
			actions = append(actions, whichKeyAction{"d", "delete"})
		}
		actions = append(actions, whichKeyAction{"", ""})
		actions = append(actions, whichKeyAction{"esc", "close"})
		return actions
	case KindPortal:
		return []whichKeyAction{
			{"⏎", "resume"},
			{"p", "resume +prompt"},
			{"", ""},
			{"esc", "close"},
		}
	}
	return nil
}

// favoriteToggleLabel describes the `f` action target: "favorite" if
// the project is currently unfavorited, "unfavorite" if it already is.
func (m *Model) favoriteToggleLabel(it *listItem) string {
	if it != nil && it.project != nil && it.project.Favorite {
		return "unfavorite"
	}
	return "favorite"
}

// favoriteToggleLabelGroup is the group-row variant. Reads the
// favorite flag from the in-memory WorkspaceData.FavoriteGroups set
// for whichever workspace owns this group.
func (m *Model) favoriteToggleLabelGroup(group string) string {
	for _, ws := range m.workspaces {
		if ws.FavoriteGroups[group] {
			return "unfavorite"
		}
	}
	return "favorite"
}

func (m *Model) updateWhichKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	item := m.currentItem()

	// Handle worktree sub-level.
	if m.whichKeyLevel == 1 {
		switch key {
		case "esc":
			m.whichKeyLevel = 0
			return m, nil
		case "n":
			if item != nil && item.kind == KindProject {
				m.wtNoLaunch = true
				m.wtBranch = ""
				m.wtField = 0
				m.popupProj = item.project
				m.mode = viewNewWorktree
				return m, nil
			}
		}
		return m, nil
	}

	// Root level — dispatch action.
	switch key {
	case "esc":
		m.mode = viewList
		return m, nil
	case "enter":
		m.mode = viewList
		return m.updateList(msg)
	case "p":
		m.mode = viewList
		return m.updateList(msg)
	case "w":
		if item != nil && item.kind == KindProject {
			m.whichKeyLevel = 1
			return m, nil
		}
		m.mode = viewList
	case "l":
		m.mode = viewList
		return m.updateList(msg)
	case "d":
		m.mode = viewList
		return m.updateList(msg)
	case "m":
		m.mode = viewList
		return m.updateList(msg)
	case "e":
		m.mode = viewList
		return m.updateList(msg)
	case "f":
		// Favorite toggle is per-row: project rows toggle the project,
		// group rows toggle the group. Either way close the panel so
		// the user sees the result immediately.
		m.mode = viewList
		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" {
			m.toggleFavoriteGroup(item.group)
		}
		return m, nil
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

// toggleFavoriteGroup flips the favorite flag on the named group in
// the workspace that owns the current cursor row. The in-memory
// WorkspaceData is updated so the chip header repaint picks up the
// change without a TUI restart. Symmetric to toggleFavoriteFor.
func (m *Model) toggleFavoriteGroup(group string) {
	root := m.workspaceRootForGroup(group)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for group"
		return
	}
	current := false
	for i := range m.workspaces {
		if m.workspaces[i].Root == root {
			current = m.workspaces[i].FavoriteGroups[group]
			break
		}
	}
	target := !current
	err := MutateAndSave(root, func(ws *config.Workspace) bool {
		return ws.SetGroupFavorite(group, target)
	})
	if err != nil {
		m.statusMsg = "favorite: " + err.Error()
		return
	}
	for i := range m.workspaces {
		if m.workspaces[i].Root != root {
			continue
		}
		if m.workspaces[i].FavoriteGroups == nil {
			m.workspaces[i].FavoriteGroups = map[string]bool{}
		}
		if target {
			m.workspaces[i].FavoriteGroups[group] = true
			m.statusMsg = "* favorited @" + group
		} else {
			delete(m.workspaces[i].FavoriteGroups, group)
			m.statusMsg = "unfavorited @" + group
		}
		break
	}
	m.rebuildItems()
	m.clampCursor()
	m.ensureVisible()
}

// workspaceRootForGroup finds the workspace whose Groups slice
// contains `name`. Returns "" when unmatched (e.g. group was just
// removed in a parallel save).
func (m *Model) workspaceRootForGroup(name string) string {
	for _, ws := range m.workspaces {
		for _, g := range ws.Groups {
			if g == name {
				return ws.Root
			}
		}
	}
	return ""
}

// toggleFavoriteFor flips the favorite flag on `proj` and persists the
// change. The in-memory pointer is mutated so the row repaint picks
// up the new state without needing a TUI restart. The header section
// is rebuilt: a freshly favorited project may need to leave Recent
// and appear in Favorites, and vice versa.
func (m *Model) toggleFavoriteFor(proj *Project) {
	root := m.workspaceRootFor(proj)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for project"
		return
	}
	target := !proj.Favorite
	err := MutateAndSave(root, func(ws *config.Workspace) bool {
		p := ws.Projects[proj.ID]
		if !p.SetFavorite(target) {
			return false
		}
		ws.Projects[proj.ID] = p
		return true
	})
	if err != nil {
		m.statusMsg = "favorite: " + err.Error()
		return
	}
	proj.Favorite = target
	if target {
		m.statusMsg = "* favorited " + proj.Name
	} else {
		m.statusMsg = "unfavorited " + proj.Name
	}
	m.rebuildItems()
	m.clampCursor()
	m.ensureVisible()
}

func (m *Model) whichKeyTitle() string {
	item := m.currentItem()
	if item == nil {
		return "actions"
	}
	if m.whichKeyLevel == 1 {
		return "worktree"
	}
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group // display name
	case KindPortal:
		if item.session != nil {
			t := item.session.Title
			if len(t) > 16 {
				t = t[:16] + "…"
			}
			return t
		}
	}
	return "actions"
}

func (m *Model) viewWhichKey() string {
	listW := 48
	if m.width < 72 {
		listW = m.width - 28
		if listW < 30 {
			listW = 30
		}
	}

	// Render the list (dimmed).
	var rows []string
	bc := m.breadcrumb()
	pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
	hdr := m.padRight(" "+bc, pos+" ", listW)
	rows = append(rows, headerStyle.Width(listW).Render(hdr))
	rows = append(rows, m.renderListRows(listW, true)...)
	rows = append(rows, footerStyle.Width(listW).Render(" press a key or esc"))

	listPanel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// Render the action panel.
	actions := m.whichKeyActions()
	title := m.whichKeyTitle()

	panelW := 20
	var actionLines []string
	actionLines = append(actionLines, whichKeyTitleStyle.Width(panelW-4).Render(title))
	actionLines = append(actionLines, "")

	for _, a := range actions {
		if a.key == "" {
			actionLines = append(actionLines, "")
			continue
		}
		keyPart := whichKeyKeyStyle.Render(a.key)
		descPart := whichKeyDescStyle.Render(" " + a.desc)
		actionLines = append(actionLines, " "+keyPart+descPart)
	}

	actionContent := strings.Join(actionLines, "\n")
	actionPanel := whichKeyBorderStyle.Width(panelW).Render(actionContent)

	// Position the action panel vertically aligned with the cursor.
	listH := lipgloss.Height(listPanel)
	panelH := lipgloss.Height(actionPanel)
	topPad := (listH - panelH) / 2
	if topPad < 0 {
		topPad = 0
	}
	paddedPanel := strings.Repeat("\n", topPad) + actionPanel

	combined := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, "  ", paddedPanel)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		combined,
	)
}
