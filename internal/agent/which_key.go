package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/tui"
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
		return []whichKeyAction{
			{"n", "new worktree"},
			{"", ""},
			{"esc", "back"},
		}
	}

	switch item.kind {
	case KindGroup:
		if item.projectionGroup {
			return []whichKeyAction{
				{"⏎", "expand"},
				{"tab", "expand"},
				{"", ""},
				{"esc", "close"},
			}
		}
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"f", m.favoriteToggleLabelGroup(item.workspaceRoot, item.group)},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"f", m.favoriteToggleLabel(item)},
			{"w", "worktree ›"},
			{"e", "edit"},
			{"l", "shell"},
			{"", ""},
			{"esc", "close"},
		}
	}
	return nil
}

func (m *Model) favoriteToggleLabel(it *listItem) string {
	if it != nil && it.project != nil && it.project.Favorite {
		return "unfavorite"
	}
	return "favorite"
}

func (m *Model) favoriteToggleLabelGroup(workspaceRoot, group string) string {
	for _, ws := range m.workspaces {
		if ws.Root == workspaceRoot && ws.FavoriteGroups[group] {
			return "unfavorite"
		}
	}
	return "favorite"
}

func (m *Model) updateWhichKey(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	item := m.currentItem()

	if m.whichKeyLevel == 1 {
		switch key {
		case "esc":
			m.whichKeyLevel = 0
			return m, nil
		case "n":
			if item != nil && item.kind == KindProject {
				m.wtBranch.SetValue("")
				m.wtBranch.Focus()
				m.wtField = 0
				m.popupProj = item.project
				m.mode = viewNewWorktree
				return m, nil
			}
		}
		return m, nil
	}

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

		m.mode = viewList
		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" && !item.projectionGroup {
			m.toggleFavoriteGroup(item.workspaceRoot, item.group)
		}
		return m, nil
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) toggleFavoriteGroup(root, group string) {
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
	metrics.RecordExplorerFavoriteChanged()
}

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
	metrics.RecordExplorerFavoriteChanged()
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
		return presentLabel(item.group)
	case KindProject:
		return presentLabel(item.project.Name)
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

	var rows []string
	bc := m.breadcrumb()
	pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
	hdr := m.padRight(" "+bc, pos+" ", listW)
	rows = append(rows, headerStyle.Width(listW).Render(hdr))
	rows = append(rows, m.renderListRows(listW, true)...)
	rows = append(rows, footerStyle.Width(listW).Render(" press a key or esc"))

	listPanel := tui.JoinVertical(tui.Left, rows...)

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

	listH := tui.Height(listPanel)
	panelH := tui.Height(actionPanel)
	topPad := (listH - panelH) / 2
	if topPad < 0 {
		topPad = 0
	}
	paddedPanel := strings.Repeat("\n", topPad) + actionPanel

	combined := tui.JoinHorizontal(tui.Top, listPanel, "  ", paddedPanel)

	return tui.Place(
		m.width, m.height,
		tui.Center, tui.Center,
		combined,
	)
}
