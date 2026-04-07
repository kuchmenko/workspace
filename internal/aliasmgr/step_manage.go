package aliasmgr

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/alias"
)

func (m Model) filtered() []int {
	q := strings.ToLower(m.search.Value())
	var idx []int
	for i, it := range m.items {
		if m.tabFilter == 1 && it.kind != kindProject {
			continue
		}
		if m.tabFilter == 2 && it.kind != kindGroup {
			continue
		}
		if m.tabFilter != 0 && it.kind == kindRoot {
			// Root row only shown in the "all" tab.
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.name), q) {
			continue
		}
		idx = append(idx, i)
	}
	return idx
}

func (m Model) maxVisible() int {
	h := m.height - 9
	if h < 5 {
		h = 5
	}
	return h
}

func (m Model) updateManage(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		filtered := m.filtered()
		switch key.String() {
		case "esc":
			m.result = Result{Cancelled: true}
			return m, tea.Quit
		case "enter":
			m.step = stepConfirm
			m.stepChangedAt = time.Now()
			return m, nil
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
			return m, nil
		case "down", "ctrl+n":
			if m.cursor < len(filtered)-1 {
				m.cursor++
				if m.cursor >= m.offset+m.maxVisible() {
					m.offset = m.cursor - m.maxVisible() + 1
				}
			}
			return m, nil
		case " ":
			if len(filtered) > 0 && m.cursor < len(filtered) {
				idx := filtered[m.cursor]
				m.items[idx].checked = !m.items[idx].checked
				if !m.items[idx].checked {
					m.items[idx].alias = ""
				}
			}
			return m, nil
		case "e":
			if len(filtered) > 0 && m.cursor < len(filtered) {
				idx := filtered[m.cursor]
				m.editTarget = idx
				m.editing = true
				cur := m.items[idx].alias
				if cur == "" {
					taken := m.takenNames(idx)
					cur = alias.Generate(m.items[idx].name, taken)
				}
				m.editInput.SetValue(cur)
				m.editInput.CursorEnd()
				return m, m.editInput.Focus()
			}
			return m, nil
		case "tab":
			m.tabFilter = (m.tabFilter + 1) % 3
			m.cursor = 0
			m.offset = 0
			return m, nil
		}
	}

	var cmd tea.Cmd
	prev := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prev {
		m.cursor = 0
		m.offset = 0
	}
	return m, cmd
}

func (m Model) updateEditing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			name := strings.TrimSpace(m.editInput.Value())
			if name != "" {
				m.items[m.editTarget].alias = name
				m.items[m.editTarget].checked = true
			}
			m.editing = false
			return m, nil
		case "esc":
			m.editing = false
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

// takenNames returns the set of alias names already in use, excluding `skip`.
func (m Model) takenNames(skip int) map[string]struct{} {
	taken := make(map[string]struct{})
	for i, it := range m.items {
		if i == skip || !it.checked || it.alias == "" {
			continue
		}
		taken[it.alias] = struct{}{}
	}
	return taken
}

func (m Model) viewManage() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws alias "))
	b.WriteString("  Manage aliases\n\n")

	b.WriteString("  " + m.search.View() + "\n")

	b.WriteString("  ")
	tabs := []string{"all", "projects", "groups"}
	for i, t := range tabs {
		if i > 0 {
			b.WriteString(" ")
		}
		if i == m.tabFilter {
			b.WriteString(activeTab.Render(t))
		} else {
			b.WriteString(inactiveTab.Render(t))
		}
	}
	b.WriteString(dimStyle.Render("  (tab)"))
	b.WriteString("\n\n")

	filtered := m.filtered()
	if len(filtered) == 0 {
		b.WriteString("  " + dimStyle.Render("no items match") + "\n")
	}
	maxV := m.maxVisible()
	end := m.offset + maxV
	if end > len(filtered) {
		end = len(filtered)
	}

	const aliasW = 14
	const nameW = 30

	for vi := m.offset; vi < end; vi++ {
		idx := filtered[vi]
		it := m.items[idx]
		isCursor := vi == m.cursor

		prefix := "  "
		if isCursor {
			prefix = cursorStyle.Render("> ")
		}
		check := uncheckStyle.Render("○")
		if it.checked {
			check = checkStyle.Render("●")
		}

		// Resolve raw alias text + how to style it.
		var aliasRaw, aliasStyled string
		switch {
		case m.editing && idx == m.editTarget:
			aliasStyled = padRight(m.editInput.View(), aliasW, len(m.editInput.Value()))
		case it.alias != "":
			aliasRaw = it.alias
			aliasStyled = padRight(selectedStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		case it.checked:
			// Preview the auto-generated name so the user sees what will be saved.
			aliasRaw = alias.Generate(it.generationSeed(), m.takenNames(idx))
			aliasStyled = padRight(dimStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		default:
			aliasRaw = "(auto)"
			aliasStyled = padRight(dimStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		}

		nameRaw := it.name
		if it.kind == kindRoot {
			nameRaw = "(workspace root)"
		}
		var nameStyled string
		if isCursor {
			nameStyled = padRight(selectedStyle.Render(nameRaw), nameW, len(nameRaw))
		} else {
			nameStyled = padRight(nameRaw, nameW, len(nameRaw))
		}

		kindLabel := dimStyle.Render(kindString(it.kind))

		b.WriteString(fmt.Sprintf("%s%s  %s  →  %s  %s\n",
			prefix, check, aliasStyled, nameStyled, kindLabel))
	}

	if len(filtered) > maxV {
		above := m.offset
		below := len(filtered) - end
		if above > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more\n", above)))
		}
		if below > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more\n", below)))
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑↓ navigate  space toggle  e edit alias  tab filter  enter next  esc cancel"))
	return b.String()
}

func kindString(k itemKind) string {
	switch k {
	case kindGroup:
		return "group"
	case kindRoot:
		return "root"
	}
	return "project"
}

// padRight pads a possibly-styled string with trailing spaces so that its
// visible width equals `width`. `visibleLen` is the length of the underlying
// raw text (without ANSI escapes). If the raw text is already wider than
// `width`, the styled string is returned unchanged.
func padRight(styled string, width, visibleLen int) string {
	if visibleLen >= width {
		return styled
	}
	return styled + spaces(width-visibleLen)
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = ' '
	}
	return string(buf)
}
