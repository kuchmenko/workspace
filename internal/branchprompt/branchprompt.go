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

	// Free-text mode: collect input, confirm on enter, back out on esc.
	if m.inputMode {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				return m, nil
			}
			picked := PickedMsg{Project: m.project, Branch: val}
			return m, func() tea.Msg { return picked }
		case "esc":
			m.inputMode = false
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// Candidate-list mode.
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
		if len(m.candidates) == 0 {
			m.inputMode = true
			return m, m.input.Focus()
		}
		picked := PickedMsg{Project: m.project, Branch: m.candidates[m.cursor]}
		return m, func() tea.Msg { return picked }
	case "i":
		m.inputMode = true
		return m, m.input.Focus()
	case "esc":
		cancelled := CancelledMsg{Project: m.project}
		return m, func() tea.Msg { return cancelled }
	}
	return m, nil
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
