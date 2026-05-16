package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/layout"
)

func (m *Model) updateNewWorktree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = viewList
		return m, nil
	case "tab", "down":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "shift+tab", "up":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "enter":
		if m.wtField == 1 { // confirm
			return m.executeNewWorktree()
		}
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "backspace":
		if m.wtField == 0 && len(m.wtBranch) > 0 {
			m.wtBranch = m.wtBranch[:len(m.wtBranch)-1]
		}
		return m, nil
	default:
		if m.wtField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.wtBranch += key
		}
	}
	return m, nil
}

func (m *Model) executeNewWorktree() (tea.Model, tea.Cmd) {
	branch := strings.TrimSpace(m.wtBranch)
	if branch == "" {
		return m, nil
	}

	wsRoot := m.workspaceRootFor(m.popupProj)
	result, err := CreateWorktree(m.popupProj, branch, wsRoot, m.popupProj.ID)
	if err != nil {
		m.statusMsg = err.Error()
		m.mode = viewList
		return m, nil
	}
	m.wtCache.Invalidate(m.popupProj.Path)

	// If "create worktree only" (w key), go back to list.
	if m.wtNoLaunch {
		m.wtNoLaunch = false
		m.mode = viewList
		m.rebuildItems()
		m.ensureVisible()
		m.statusMsg = "worktree created"
		return m, nil
	}

	// Go to prompt input before launching.
	m.pendingLaunch = &LaunchRequest{Cwd: result.Path}
	m.promptInput = ""
	m.mode = viewPromptInput
	return m, nil
}

func (m *Model) viewNewWorktree() string {
	p := m.popupProj
	popupW := 50
	if m.width < 56 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("%s New worktree for %s", iconWorktree, p.Name)))
	lines = append(lines, "")

	// Field 0: branch (single input — user types the literal branch name).
	branchLabel := "  Branch name:"
	branchVal := m.wtBranch + "█"
	if m.wtField != 0 {
		branchVal = m.wtBranch
		if branchVal == "" {
			branchVal = "(required)"
		}
	}
	if m.wtField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+branchVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+branchVal))
	}
	if branch := strings.TrimSpace(m.wtBranch); branch != "" {
		pathPreview := fmt.Sprintf("  → dir: %s-wt-<machine>-%s", p.Name, layout.SlugifyBranch(branch))
		lines = append(lines, popupDimStyle.Width(innerW).Render(pathPreview))
	}
	lines = append(lines, "")

	// Field 1: confirm button
	confirmLabel := "  → Create worktree"
	if m.wtField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(confirmLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(confirmLabel))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  enter:confirm  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
}

func (m *Model) updatePromptInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
		m.pendingLaunch = nil
	case "enter":
		// Launch with or without prompt.
		m.pendingLaunch.Prompt = strings.TrimSpace(m.promptInput)
		m.Launch = m.pendingLaunch
		m.pendingLaunch = nil
		return m, tea.Quit
	case "backspace":
		if len(m.promptInput) > 0 {
			m.promptInput = m.promptInput[:len(m.promptInput)-1]
		}
	default:
		if len(key) == 1 && key[0] >= 32 {
			m.promptInput += key
		} else if key == "space" || key == " " {
			m.promptInput += " "
		}
	}
	return m, nil
}

func (m *Model) viewPromptInput() string {
	if m.pendingLaunch == nil {
		m.mode = viewList
		return m.viewList()
	}
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render("Launch claude"))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("in: %s", m.pendingLaunch.Cwd)))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  Initial prompt (optional):"))

	input := m.promptInput + "█"
	lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+input))
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Enter: launch (empty = interactive)"))
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Esc: back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
}
