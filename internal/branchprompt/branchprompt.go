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

// Model is a standalone bubbletea model. It is a value type — callers
// re-assign after each Update, the same convention bubbles/* uses.
type Model struct {
	project    string
	candidates []string
	cursor     int
	inputMode  bool
	input      textinput.Model
}

// NewModel constructs a Model for the given project with the given branch
// candidates. candidates may be empty — the model auto-enters free-text
// mode when the user presses enter on an empty list.
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

// Init returns no initial command; the parent is expected to have already
// switched steps and rendered a frame before this model is consulted.
func (m Model) Init() tea.Cmd { return nil }

// Update handles keystrokes. Non-key messages are ignored (the parent's
// Update handles spinners, window resizes, etc.).
//
// On pick/cancel, Update emits PickedMsg or CancelledMsg via a returned
// tea.Cmd. The parent Update is expected to recognize these messages
// and act on them (unblock a channel, change step, etc.). Update does
// NOT mutate any non-UI state of the parent — all side effects flow
// through the emitted message.
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

// updateInputMode handles keystrokes while the user is typing a
// free-text branch name. Enter confirms (emits PickedMsg with the
// trimmed value, no-op on empty), Esc returns to the candidate list,
// any other key forwards to the underlying textinput.
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

// updateListMode handles keystrokes while the user is browsing the
// candidate-branch list. j/k or up/down move the cursor; Enter
// confirms (or falls through to input mode when the list is empty);
// i opens input mode unconditionally; Esc cancels the prompt.
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

// confirmListSelection commits the highlighted candidate as the
// picked branch. When the candidate list is empty the user is
// dropped into input mode so they can type one.
func (m Model) confirmListSelection() (Model, tea.Cmd) {
	if len(m.candidates) == 0 {
		m.inputMode = true
		return m, m.input.Focus()
	}
	return m, emitPickedCmd(m.project, m.candidates[m.cursor])
}

// emitPickedCmd builds a tea.Cmd that emits PickedMsg. Centralized
// so the closure form lives in one place rather than scattered
// across two return sites.
func emitPickedCmd(project, branch string) tea.Cmd {
	picked := PickedMsg{Project: project, Branch: branch}
	return func() tea.Msg { return picked }
}

// emitCancelledCmd is the symmetric helper for CancelledMsg.
func emitCancelledCmd(project string) tea.Cmd {
	canceled := CancelledMsg{Project: project}
	return func() tea.Msg { return canceled }
}

// Project returns the project name this prompt is for — useful for
// headers rendered by the caller outside of this model's View.
func (m Model) Project() string { return m.project }

// View renders the prompt using the shared palette below. Callers that
// want a different look should wrap this in their own styling rather
// than reach into the model.
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

// Styles mirror the palette used by cli/bootstrap.go so the visual
// language stays consistent after extraction. Keeping a private copy
// here (rather than importing from cli) keeps the dependency graph
// simple — branchprompt is a leaf.
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
