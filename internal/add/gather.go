package add

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// handleSourceDone folds one source's FetchSuggestions outcome into
// the model. Called for every source as it completes (sources run in
// parallel via separate tea.Cmds from Init), so this runs ~N times
// per session where N == len(m.sources).
//
// State transitions:
//   - First source with results → addStateGathering → addStateBrowse
//     (user sees something the moment any source finishes)
//   - Last source done with no cumulative results → addStateBrowseEmpty
//   - Subsequent sources after browse is reached → silently fold in;
//     the rendered tree updates next frame
func (m AddModel) handleSourceDone(msg sourceDoneMsg) (tea.Model, tea.Cmd) {
	m.sourcesDone++
	m.sourceOutcomes = append(m.sourceOutcomes, SourceOutcome{
		Name:     msg.name,
		Count:    len(msg.items),
		Duration: msg.took,
		Err:      msg.err,
	})
	if msg.err == nil && len(msg.items) > 0 {
		// Re-run dedup against the existing list so a repo that
		// shows up in two sources merges into one row even if the
		// sources finish on different ticks.
		merged := mergeSuggestions([][]Suggestion{m.allSuggestions, msg.items})
		sortByRelevance(merged)
		m.allSuggestions = merged
		// Cursor may need clamping if the dedup pass shrank an
		// already-rendered list (rare but possible if a clipboard
		// suggestion arrives last and merges with an existing GH
		// suggestion).
		if m.cursor >= len(m.allSuggestions) && len(m.allSuggestions) > 0 {
			m.cursor = len(m.allSuggestions) - 1
		}
	}

	// State decisions only apply while we're still on the gathering
	// screen — sources finishing after the user has already entered
	// manual/edit/confirm don't yank them back.
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
		// Show progress so the user can tell we haven't hung — e.g.
		// "(2/3 sources done)".
		fmt.Fprintf(&b, " %s", addDim.Render(fmt.Sprintf("(%d/%d done)", m.sourcesDone, len(m.sources))))
	}
	b.WriteString("\n\n")
	// Per-source progress chips — same look as the in-browse line.
	if len(m.sourceOutcomes) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSourceChipsLive(m.sourceOutcomes))
		b.WriteString("\n\n")
	}
	b.WriteString("  " + addHelp.Render("[ctrl+c] cancel"))
	return b.String()
}
