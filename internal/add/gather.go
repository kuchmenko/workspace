package add

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m AddModel) handleSourceDone(msg sourceDoneMsg) (tea.Model, tea.Cmd) {
	m.sourcesDone++
	m.sourceOutcomes = append(m.sourceOutcomes, SourceOutcome{
		Name:     msg.name,
		Count:    len(msg.items),
		Duration: msg.took,
		Err:      msg.err,
	})
	if msg.err == nil && len(msg.items) > 0 {
		merged := mergeSuggestions([][]Suggestion{m.allSuggestions, msg.items})
		sortByRelevance(merged)
		m.allSuggestions = merged

		if m.cursor >= len(m.allSuggestions) && len(m.allSuggestions) > 0 {
			m.cursor = len(m.allSuggestions) - 1
		}
	}

	if m.state == addStateGathering {
		switch {
		case len(m.allSuggestions) > 0:
			m.transitionTo(addStateBrowse)
		case m.sourcesDone >= len(m.sources):
			m.transitionTo(addStateBrowseEmpty)
		}
	}
	return m, nil
}

func (m AddModel) updateGathering(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m AddModel) viewGathering() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Add project — gathering "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.spinner.View() + " probing sources")
	if m.sourcesDone > 0 {
		fmt.Fprintf(&b, " %s", addDim.Render(fmt.Sprintf("(%d/%d done)", m.sourcesDone, len(m.sources))))
	}
	b.WriteString("\n\n")

	if len(m.sourceOutcomes) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSourceChipsLive(m.sourceOutcomes))
		b.WriteString("\n\n")
	}
	b.WriteString("  " + addHelp.Render("[ctrl+c] cancel"))
	return b.String()
}
