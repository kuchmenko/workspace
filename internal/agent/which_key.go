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
				{"⏎/l", "open"},
				{"tab", "expand"},
				{"g/G", "first/last"},
				{"^d/^u", "half-page"},
				{"^f/^b", "page"},
				{"", ""},
				{"esc", "close"},
			}
		}
		return []whichKeyAction{
			{"⏎/l", "open sheet"},
			{"f", m.favoriteToggleLabelGroup(item.workspaceRoot, item.group)},
			{"A", "jobs"},
			{"M", "maintenance"},
			{"tab", "expand"},
			{"g/G", "first/last"},
			{"^d/^u", "half-page"},
			{"^f/^b", "page"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"⏎/l", "open sheet"},
			{"f", m.favoriteToggleLabel(item)},
			{"A", "jobs"},
			{"M", "maintenance"},
			{"w", "worktree ›"},
			{"e", "edit"},
			{"g/G", "first/last"},
			{"^d/^u", "half-page"},
			{"^f/^b", "page"},
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
	case "l", "right":
		m.mode = viewList
		return m.updateList(msg)
	case "g", "G", "home", "end", "ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b", "pgdn", "pgup":
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
			return m, m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" && !item.projectionGroup {
			return m, m.toggleFavoriteGroup(item.workspaceRoot, item.group)
		}
		return m, nil
	case "A", "M":
		m.mode = viewList
		return m.updateList(msg)
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) toggleFavoriteGroup(root, group string) tui.Cmd {
	if root == "" {
		m.statusMsg = "cannot resolve workspace for group"
		return nil
	}
	return m.submitJob("favorite @"+group, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		ctx.withRegistry(root, func() {
			ws, err := config.Load(root)
			if err != nil {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: err.Error()}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			current, ok := ws.Groups[group]
			if !ok {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: "group is not declared in workspace.toml"}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			current.Favorite = !current.Favorite
			ws.Groups[group] = current
			if err := config.Save(root, ws); err != nil {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: err.Error()}
			} else {
				outcome = targetOutcome{Target: group, Kind: targetSuccess, Detail: "saved"}
			}
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{WorkspaceRoot: root}}}, outcome.Kind == targetSuccess)
		})
		metrics.RecordExplorerFavoriteChanged()
		return jobResult{Summary: "favorite updated", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{WorkspaceRoot: root}}}
	})
}

func (m *Model) toggleFavoriteFor(proj *Project) tui.Cmd {
	root := m.workspaceRootFor(proj)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for project"
		return nil
	}
	projectID, name := proj.ID, proj.Name
	return m.submitJob("favorite "+name, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		ctx.withRegistry(root, func() {
			ws, err := config.Load(root)
			if err != nil {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: err.Error()}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			p := ws.Projects[projectID]
			if _, ok := ws.Projects[projectID]; !ok {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: "project is missing from workspace.toml"}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			p.SetFavorite(!p.Favorite)
			ws.Projects[projectID] = p
			if err := config.Save(root, ws); err != nil {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: err.Error()}
			} else {
				outcome = targetOutcome{Target: name, Kind: targetSuccess, Detail: "saved"}
			}
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{root, projectID}}}, outcome.Kind == targetSuccess)
		})
		metrics.RecordExplorerFavoriteChanged()
		return jobResult{Summary: "favorite updated", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{root, projectID}}}
	})
}

func outcomeError(outcome targetOutcome) string {
	if outcome.Kind == targetFailed || outcome.Kind == targetPartial {
		return outcome.Detail
	}
	return ""
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
