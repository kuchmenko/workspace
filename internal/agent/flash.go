package agent

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// jumpLabels is the alphabet used for flash jump labels.
const jumpLabels = "asdfghjklqwertyuiopzxcvbnm"

func (m *Model) updateFlash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.exitFlash(false)
	case "backspace":
		if len(m.flashQuery) > 0 {
			m.flashQuery = m.flashQuery[:len(m.flashQuery)-1]
			m.recomputeFlash()
		} else {
			m.exitFlash(false)
		}
	case "enter":
		// Jump to first match.
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[0]
			m.ensureVisible()
		}
		m.exitFlash(true)
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			ch := rune(key[0])
			// Check if this character is a non-conflicting jump label.
			// Labels are only assigned from characters that would NOT
			// match if appended to the query, so this is unambiguous.
			if m.flashQuery != "" {
				for i, label := range m.flashLabels {
					if label != 0 && ch == label && i < len(m.flashMatches) {
						m.cursor = m.flashMatches[i]
						m.ensureVisible()
						m.exitFlash(true)
						return m, nil
					}
				}
			}
			// Not a label — append to query to narrow results.
			m.flashQuery += key
			m.recomputeFlash()
		}
	}
	return m, nil
}

// exitFlash leaves flash mode. For global search (S), if the user
// canceled (jumped=false), restore the original expansion state.
// If they jumped to an item, keep expansions so the target is visible.
func (m *Model) exitFlash(jumped bool) {
	m.mode = viewList
	if m.flashGlobal && !jumped && m.savedExpanded != nil {
		m.expanded = m.savedExpanded
		m.savedExpanded = nil
		m.rebuildItems()
		m.ensureVisible()
	}
	m.flashGlobal = false
}

func (m *Model) recomputeFlash() {
	query := strings.ToLower(m.flashQuery)
	m.flashMatches = nil
	m.flashLabels = nil

	// Collect matches. Section rows are non-selectable and must never
	// appear in the flash match list — pressing a label that targets
	// a section row would be a no-op and confuse the user.
	for i, item := range m.items {
		if !item.isSelectable() {
			continue
		}
		name := m.itemSearchName(item)
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			m.flashMatches = append(m.flashMatches, i)
		}
	}

	// Compute non-conflicting labels: only use characters that, when
	// appended to the current query, would NOT match any item. This
	// makes label presses unambiguous — they can never be mistaken for
	// "continue typing to narrow results".
	available := m.availableJumpLabels()
	for i := 0; i < len(m.flashMatches); i++ {
		if i < len(available) {
			m.flashLabels = append(m.flashLabels, available[i])
		} else {
			m.flashLabels = append(m.flashLabels, 0) // no label — need more query chars
		}
	}
}

// availableJumpLabels returns characters safe to use as jump labels:
// letters that, if appended to the current query, would produce zero
// matches. This guarantees pressing a label always means "jump", never
// "keep filtering".
func (m *Model) availableJumpLabels() []rune {
	query := strings.ToLower(m.flashQuery)
	if query == "" {
		return nil // no labels until user types at least one char
	}
	var available []rune
	for _, r := range jumpLabels {
		extended := query + string(r)
		productive := false
		for _, item := range m.items {
			name := strings.ToLower(m.itemSearchName(item))
			if strings.Contains(name, extended) {
				productive = true
				break
			}
		}
		if !productive {
			available = append(available, r)
		}
	}
	return available
}

// itemSearchName returns the searchable text for a list item.
func (m *Model) itemSearchName(item listItem) string {
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group // display name
	case KindPortal:
		if item.session != nil {
			return item.session.Title
		}
	}
	return ""
}

// flashInlineLabel highlights the query match in a name and, when a
// non-zero label is available, overlays it on the character after the
// match. When label is 0 (no label assigned yet), only the match is
// highlighted — the user needs to type more chars.
func flashInlineLabel(name, query string, label rune) string {
	if query == "" {
		return name
	}
	lower := strings.ToLower(name)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 {
		return name
	}
	matchEnd := idx + len(q)
	runes := []rune(name)

	var b strings.Builder
	if idx > 0 {
		b.WriteString(string(runes[:idx]))
	}
	b.WriteString(flashMatchStyle.Render(string(runes[idx:matchEnd])))
	if label != 0 {
		// Overlay label on the next character.
		b.WriteString(flashLabelStyle.Render(string(label)))
		if matchEnd+1 < len(runes) {
			b.WriteString(string(runes[matchEnd+1:]))
		}
	} else {
		// No label — just show the rest of the name.
		if matchEnd < len(runes) {
			b.WriteString(string(runes[matchEnd:]))
		}
	}
	return b.String()
}
