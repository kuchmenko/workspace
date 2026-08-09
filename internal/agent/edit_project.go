package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func EditProjectMetadata(wsRoot, projID, group string, category config.Category) error {
	if wsRoot == "" {
		return fmt.Errorf("workspace root required")
	}
	if projID == "" {
		return fmt.Errorf("project id required")
	}
	if category != config.CategoryPersonal && category != config.CategoryWork {
		return fmt.Errorf("category must be %q or %q", config.CategoryPersonal, config.CategoryWork)
	}

	ws, err := config.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("load workspace.toml: %w", err)
	}
	proj, ok := ws.Projects[projID]
	if !ok {
		return fmt.Errorf("project %q not found in workspace.toml", projID)
	}
	proj.Group = strings.TrimSpace(group)
	proj.Category = category
	ws.Projects[projID] = proj

	if err := config.Save(wsRoot, ws); err != nil {
		return fmt.Errorf("save workspace.toml: %w", err)
	}
	return nil
}

func existingGroups(workspaces []WorkspaceData) []string {
	seen := map[string]bool{}
	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			if g != "" {
				seen[g] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func (m *Model) updateEditProject(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.editGroup.Blur()
		m.mode = viewList
		m.editErr = ""
		return m, nil
	case "tab", "down":
		m.editField = (m.editField + 1) % 3
		if m.editField == 0 {
			return m, m.editGroup.Focus()
		}
		m.editGroup.Blur()
		return m, nil
	case "shift+tab", "up":
		m.editField = (m.editField + 2) % 3
		if m.editField == 0 {
			return m, m.editGroup.Focus()
		}
		m.editGroup.Blur()
		return m, nil
	case "enter":
		if m.editField == 2 {
			return m.executeEditProject()
		}
		m.editField = (m.editField + 1) % 3
		m.editGroup.Blur()
		return m, nil
	case " ":
		if m.editField == 1 {
			if m.editCategory == config.CategoryPersonal {
				m.editCategory = config.CategoryWork
			} else {
				m.editCategory = config.CategoryPersonal
			}
			return m, nil
		}
		if m.editField == 0 {
			var cmd tui.Cmd
			m.editGroup, cmd = m.editGroup.Update(msg)
			return m, cmd
		}
	default:
		if m.editField == 0 {
			var cmd tui.Cmd
			m.editGroup, cmd = m.editGroup.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) executeEditProject() (tui.Model, tui.Cmd) {
	proj := m.popupProj
	if proj == nil {
		m.mode = viewList
		return m, nil
	}
	wsRoot := m.workspaceRootFor(proj)
	if wsRoot == "" {
		m.editErr = "workspace root not found"
		return m, nil
	}

	newGroup := strings.TrimSpace(m.editGroup.Value())
	newCat := m.editCategory

	projectID, name := proj.ID, proj.Name
	m.editErr = ""
	m.editGroup.Blur()
	m.mode = viewList
	cmd := m.submitJob("edit "+name, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		ctx.withRegistry(wsRoot, func() {
			if err := EditProjectMetadata(wsRoot, projectID, newGroup, newCat); err != nil {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: err.Error()}
			} else {
				outcome = targetOutcome{Target: name, Kind: targetSuccess, Detail: "saved"}
			}
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{wsRoot, projectID}}}, outcome.Kind == targetSuccess)
		})
		return jobResult{Summary: fmt.Sprintf("updated %s: group=%s category=%s", name, displayGroup(newGroup), newCat), Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{wsRoot, projectID}}}
	})
	return m, cmd
}

func displayGroup(g string) string {
	if g == "" {
		return "(none)"
	}
	return g
}

func recomputeGroups(projects []Project) []string {
	seen := map[string]bool{}
	for _, p := range projects {
		if p.Group != "" {
			seen[p.Group] = true
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func (m *Model) viewEditProject() string {
	p := m.popupProj
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	title := "Edit project"
	if p != nil {
		title = fmt.Sprintf("Edit project: %s", p.Name)
	}
	lines = append(lines, popupTitleStyle.Width(innerW).Render(title))
	lines = append(lines, "")

	groupLabel := "  Group:"
	groupVal := m.editGroup.Value()
	if m.editField == 0 {
		groupVal = m.editGroup.View()
	} else if groupVal == "" {
		groupVal = "(none)"
	}
	if m.editField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(groupLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+groupVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(groupLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+groupVal))
	}
	if hint := groupHint(m.workspaces); hint != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render("  existing: "+hint))
	}
	lines = append(lines, "")

	catLabel := "  Category:"
	catVal := string(m.editCategory) + "   (space to toggle: personal | work)"
	if m.editField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(catLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+string(m.editCategory)))
		lines = append(lines, popupDimStyle.Width(innerW).Render("    space toggles personal | work"))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(catLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+catVal))
	}
	lines = append(lines, "")

	saveLabel := "  → Save"
	if m.editField == 2 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(saveLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(saveLabel))
	}

	if m.editErr != "" {
		lines = append(lines, "")
		lines = append(lines, popupTitleStyle.Width(innerW).Render("error: "+m.editErr))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  space:toggle  enter:save  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}

func groupHint(workspaces []WorkspaceData) string {
	groups := existingGroups(workspaces)
	if len(groups) == 0 {
		return ""
	}
	const max = 5
	if len(groups) > max {
		groups = append(groups[:max], "…")
	}
	return strings.Join(groups, " · ")
}
