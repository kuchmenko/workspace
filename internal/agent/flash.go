package agent

import (
	"github.com/kuchmenko/workspace/internal/tui"
	"strings"
)

const jumpLabels = "asdfghjklqwertyuiopzxcvbnm"

func (m *Model) updateFlash(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tui.Quit
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

		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[0]
			m.ensureVisible()
		}
		m.exitFlash(true)
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			ch := rune(key[0])

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

			m.flashQuery += key
			m.recomputeFlash()
		}
	}
	return m, nil
}

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

	for i, item := range m.items {
		name := m.itemSearchName(item)
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			m.flashMatches = append(m.flashMatches, i)
		}
	}

	available := m.availableJumpLabels()
	for i := 0; i < len(m.flashMatches); i++ {
		if i < len(available) {
			m.flashLabels = append(m.flashLabels, available[i])
		} else {
			m.flashLabels = append(m.flashLabels, 0)
		}
	}
}

func (m *Model) availableJumpLabels() []rune {
	query := strings.ToLower(m.flashQuery)
	if query == "" {
		return nil
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

func (m *Model) itemSearchName(item listItem) string {
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group
	case KindPortal:
		if item.session != nil {
			return item.session.Title
		}
	}
	return ""
}

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
		b.WriteString(flashLabelStyle.Render(string(label)))
		if matchEnd+1 < len(runes) {
			b.WriteString(string(runes[matchEnd+1:]))
		}
	} else {
		if matchEnd < len(runes) {
			b.WriteString(string(runes[matchEnd:]))
		}
	}
	return b.String()
}
