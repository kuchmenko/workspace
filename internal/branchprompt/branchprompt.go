// Package branchprompt provides a standalone bubbletea model for picking
// a default branch when clone.CloneIntoLayout cannot auto-resolve one.
//
// This package exists to be a leaf in the import graph: both
// internal/cli/bootstrap.go and the future internal/add package need the
// same candidate-list + free-text-input UI, and embedding it via a shared
// package avoids a cycle.
//
// Callers embed Model inside their own tea.Model and delegate Update/View
// when the parent step is "branch-prompt". When the user picks a branch
// or cancels, the model emits PickedMsg / CancelledMsg; the parent is
// responsible for unblocking whichever goroutine is waiting on the answer
// (typically via a channel passed into clone.Options.PromptDefaultBranch).
package branchprompt

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	project    string
	candidates []string
	cursor     int
	inputMode  bool
	input      textinput.Model
}

func NewModel(project string, candidates []string) Model {
	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 80
	return Model{
		project:    project,
		candidates: candidates,
		input:      ti,
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.inputMode {
		return m.updateInputMode(msg, key)
	}
	return m.updateListMode(key)
}

func (m Model) updateInputMode(msg tea.Msg, key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		if val == "" {
			return m, nil
		}
		return m, emitPickedCmd(m.project, val)
	case "esc":
		m.inputMode = false
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateListMode(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.candidates)-1 {
			m.cursor++
		}
	case "enter":
		return m.confirmListSelection()
	case "i":
		m.inputMode = true
		return m, m.input.Focus()
	case "esc":
		return m, emitCancelledCmd(m.project)
	}
	return m, nil
}

func (m Model) confirmListSelection() (Model, tea.Cmd) {
	if len(m.candidates) == 0 {
		m.inputMode = true
		return m, m.input.Focus()
	}
	return m, emitPickedCmd(m.project, m.candidates[m.cursor])
}

func emitPickedCmd(project, branch string) tea.Cmd {
	picked := PickedMsg{Project: project, Branch: branch}
	return func() tea.Msg { return picked }
}

func emitCancelledCmd(project string) tea.Cmd {
	canceled := CancelledMsg{Project: project}
	return func() tea.Msg { return canceled }
}

func (m Model) Project() string { return m.project }

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Default branch needed "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  Project: %s\n\n", headerStyle.Render(m.project))

	if m.inputMode {
		b.WriteString("  Enter branch name:\n\n")
		b.WriteString("    " + m.input.View() + "\n\n")
		b.WriteString(helpStyle.Render("[enter] confirm   [esc] back to list"))
		return b.String()
	}

	if len(m.candidates) == 0 {
		b.WriteString(dimStyle.Render("  No candidates found.\n\n"))
	} else {
		b.WriteString("  Select default branch:\n\n")
		for i, c := range m.candidates {
			cursor := "  "
			line := c
			if i == m.cursor {
				cursor = cursorStyle.Render("▸ ")
				line = selectedStyle.Render(c)
			}
			fmt.Fprintf(&b, "    %s%s\n", cursor, line)
		}
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render("[↑↓] move   [enter] pick   [i] type custom   [esc] skip project"))
	return b.String()
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))
)
