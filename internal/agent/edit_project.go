package agent

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/config"
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

func (m *Model) updateEditProject(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
		m.editErr = ""
		return m, nil
	case "tab", "down":
		m.editField = (m.editField + 1) % 3
		return m, nil
	case "shift+tab", "up":
		m.editField = (m.editField + 2) % 3
		return m, nil
	case "enter":
		if m.editField == 2 {
			return m.executeEditProject()
		}
		m.editField = (m.editField + 1) % 3
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
			m.editGroup += " "
			return m, nil
		}
	case "backspace":
		if m.editField == 0 && len(m.editGroup) > 0 {
			m.editGroup = m.editGroup[:len(m.editGroup)-1]
		}
		return m, nil
	default:
		if m.editField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.editGroup += key
		}
	}
	return m, nil
}

func (m *Model) executeEditProject() (tea.Model, tea.Cmd) {
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

	newGroup := strings.TrimSpace(m.editGroup)
	newCat := m.editCategory

	if err := EditProjectMetadata(wsRoot, proj.ID, newGroup, newCat); err != nil {
		m.editErr = err.Error()
		return m, nil
	}

	for wi := range m.workspaces {
		if m.workspaces[wi].Root != wsRoot {
			continue
		}
		ws := &m.workspaces[wi]
		for pi := range ws.Projects {
			if ws.Projects[pi].ID != proj.ID {
				continue
			}
			ws.Projects[pi].Group = newGroup
			ws.Projects[pi].Category = string(newCat)
			break
		}
		ws.Groups = recomputeGroups(ws.Projects)
		if newGroup != "" {
			m.expanded[newGroup] = true
		}
		break
	}

	m.editErr = ""
	m.mode = viewList
	m.statusMsg = fmt.Sprintf("updated %s: group=%s category=%s",
		proj.Name, displayGroup(newGroup), newCat)
	m.rebuildItems()

	m.jumpToProject(proj.ID)
	m.ensureVisible()
	return m, nil
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
	groupVal := m.editGroup
	if m.editField == 0 {
		groupVal += "█"
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
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
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
