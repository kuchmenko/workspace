package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) updateChipAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.chipTarget == nil {
		m.mode = viewList
		return m, nil
	}
	target := *m.chipTarget
	switch msg.String() {
	case "esc", "q":
		m.chipTarget = nil
		m.mode = viewList
		return m, nil
	case "c", "enter":
		m.Launch = &LaunchRequest{Cwd: target.Path}
		return m, tea.Quit
	case "s", "l":
		m.Launch = &LaunchRequest{Cwd: target.Path, ShellOnly: true}
		return m, tea.Quit
	case "p":
		m.pendingLaunch = &LaunchRequest{Cwd: target.Path}
		m.promptInput = ""
		m.chipTarget = nil
		m.mode = viewPromptInput
		return m, nil
	case "w":

		if target.Kind == KindProject && target.Project != nil {
			m.popupProj = target.Project
			m.wtBranch = ""
			m.wtField = 0
			m.wtNoLaunch = true
			m.chipTarget = nil
			m.mode = viewNewWorktree
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) viewChipAction() string {
	if m.chipTarget == nil {
		return m.viewList()
	}
	target := *m.chipTarget
	popupW := 44
	if m.width < 50 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	kindLabel := "project"
	if target.Kind == KindGroup {
		kindLabel = "group"
	}

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("Launch %s", kindLabel)))
	lines = append(lines, popupDimStyle.Width(innerW).Render(target.Name))
	lines = append(lines, popupDimStyle.Width(innerW).Render(target.Path))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  c / ⏎  claude"))
	lines = append(lines, popupItemStyle.Width(innerW).Render("  p     claude + prompt"))
	lines = append(lines, popupItemStyle.Width(innerW).Render("  s / l shell"))
	if target.Kind == KindProject {
		lines = append(lines, popupItemStyle.Width(innerW).Render("  w     new worktree"))
	}
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("  esc cancel"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
}
