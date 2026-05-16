package add

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

func (m AddModel) updateManual(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.manualInput.Value())
			if val == "" {
				m.manualErr = "URL is required"
				return m, nil
			}
			// Build editFields from the bare URL.
			name := parseRepoNameFromURL(val)
			m.editFields = editFields{
				Name:     name,
				URL:      val,
				Category: config.CategoryPersonal,
				Group:    "",
				Path:     buildPath("", config.CategoryPersonal, name),
			}
			m.editFocus = 0
			m.editErr = ""
			m.transitionTo(addStateEdit)
			return m, nil
		case "esc":
			m.transitionTo(addStateBrowse)
			m.manualInput.Blur()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.manualInput, cmd = m.manualInput.Update(msg)
	return m, cmd
}

func (m AddModel) viewManual() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Manual URL "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.manualInput.View() + "\n")
	if m.manualErr != "" {
		b.WriteString("\n  " + addErr.Render(m.manualErr) + "\n")
	}
	b.WriteString("\n  " + addHelp.Render("[⏎] continue   [esc] back"))
	return b.String()
}
