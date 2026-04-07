package aliasmgr

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/alias"
)

// treeRow is one rendered line of the tree: a reference to an item plus
// the indent/branch prefix to print before it. The cursor and offset
// operate over the slice of treeRows produced by buildTree.
type treeRow struct {
	itemIdx int
	prefix  string // branch art (e.g. "├── " / "│   └── ")
}

// itemIndex builds a map keyed by (kind,name) so we can find the slice
// position of a given project/group/root item quickly.
func (m Model) itemIndex() map[string]int {
	out := make(map[string]int, len(m.items))
	for i, it := range m.items {
		out[itemKey(it.kind, it.name)] = i
	}
	return out
}

func itemKey(k itemKind, name string) string {
	return fmt.Sprintf("%d/%s", k, name)
}

// buildTree returns the ordered list of tree rows to render, applying the
// current search filter. The structure is:
//
//	(workspace root)
//	├── group-a
//	│   ├── project-1
//	│   └── project-2
//	├── group-b
//	│   └── project-3
//	├── ungrouped-project-1
//	└── ungrouped-project-2
func (m Model) buildTree() []treeRow {
	idx := m.itemIndex()
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))

	// Group projects by their group name; collect ungrouped separately.
	grouped := make(map[string][]string)
	var ungrouped []string
	for name, p := range m.ws.Projects {
		if p.Group != "" {
			if _, ok := m.ws.Groups[p.Group]; ok {
				grouped[p.Group] = append(grouped[p.Group], name)
				continue
			}
		}
		ungrouped = append(ungrouped, name)
	}
	for _, list := range grouped {
		sort.Strings(list)
	}
	sort.Strings(ungrouped)

	// Group names ordered alphabetically.
	groupNames := make([]string, 0, len(m.ws.Groups))
	for g := range m.ws.Groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	matches := func(name string) bool {
		if q == "" {
			return true
		}
		return strings.Contains(strings.ToLower(name), q)
	}

	var rows []treeRow
	rootIdx, hasRoot := idx[itemKey(kindRoot, alias.RootTarget)]
	if hasRoot {
		rows = append(rows, treeRow{itemIdx: rootIdx, prefix: ""})
	}

	// Filter groups: keep group if its name matches OR any project under it matches.
	type visibleGroup struct {
		name     string
		projects []string
	}
	var visGroups []visibleGroup
	for _, g := range groupNames {
		groupMatches := matches(g)
		var keep []string
		for _, p := range grouped[g] {
			if groupMatches || matches(p) {
				keep = append(keep, p)
			}
		}
		if groupMatches || len(keep) > 0 {
			// If group itself matches but no project filter, show all of its projects.
			if groupMatches && q != "" && len(keep) == 0 {
				keep = append([]string{}, grouped[g]...)
			}
			if q == "" {
				keep = append([]string{}, grouped[g]...)
			}
			visGroups = append(visGroups, visibleGroup{name: g, projects: keep})
		}
	}

	var visUngrouped []string
	for _, p := range ungrouped {
		if matches(p) {
			visUngrouped = append(visUngrouped, p)
		}
	}

	// Render tree under root.
	totalTop := len(visGroups) + len(visUngrouped)
	pos := 0
	for _, vg := range visGroups {
		isLastTop := pos == totalTop-1
		branch := "├── "
		if isLastTop {
			branch = "└── "
		}
		if gi, ok := idx[itemKey(kindGroup, vg.name)]; ok {
			rows = append(rows, treeRow{itemIdx: gi, prefix: branch})
		}
		// Children
		childIndent := "│   "
		if isLastTop {
			childIndent = "    "
		}
		for j, p := range vg.projects {
			isLastChild := j == len(vg.projects)-1
			childBranch := childIndent + "├── "
			if isLastChild {
				childBranch = childIndent + "└── "
			}
			if pi, ok := idx[itemKey(kindProject, p)]; ok {
				rows = append(rows, treeRow{itemIdx: pi, prefix: childBranch})
			}
		}
		pos++
	}
	for _, p := range visUngrouped {
		isLastTop := pos == totalTop-1
		branch := "├── "
		if isLastTop {
			branch = "└── "
		}
		if pi, ok := idx[itemKey(kindProject, p)]; ok {
			rows = append(rows, treeRow{itemIdx: pi, prefix: branch})
		}
		pos++
	}

	return rows
}

func (m Model) maxVisible() int {
	h := m.height - 8
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
		rows := m.buildTree()
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
			if m.cursor < len(rows)-1 {
				m.cursor++
				if m.cursor >= m.offset+m.maxVisible() {
					m.offset = m.cursor - m.maxVisible() + 1
				}
			}
			return m, nil
		case " ":
			if len(rows) > 0 && m.cursor < len(rows) {
				idx := rows[m.cursor].itemIdx
				m.items[idx].checked = !m.items[idx].checked
				if !m.items[idx].checked {
					m.items[idx].alias = ""
				}
			}
			return m, nil
		case "e":
			if len(rows) > 0 && m.cursor < len(rows) {
				idx := rows[m.cursor].itemIdx
				m.editTarget = idx
				m.editing = true
				cur := m.items[idx].alias
				if cur == "" {
					taken := m.takenNames(idx)
					cur = alias.Generate(m.items[idx].generationSeed(), taken)
				}
				m.editInput.SetValue(cur)
				m.editInput.CursorEnd()
				return m, m.editInput.Focus()
			}
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

	b.WriteString("  " + m.search.View() + "\n\n")

	rows := m.buildTree()
	if len(rows) == 0 {
		b.WriteString("  " + dimStyle.Render("no items match") + "\n")
	}
	maxV := m.maxVisible()
	end := m.offset + maxV
	if end > len(rows) {
		end = len(rows)
	}

	const aliasW = 14

	for vi := m.offset; vi < end; vi++ {
		row := rows[vi]
		idx := row.itemIdx
		it := m.items[idx]
		isCursor := vi == m.cursor

		cursor := "  "
		if isCursor {
			cursor = cursorStyle.Render("> ")
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
		switch {
		case isCursor:
			nameStyled = selectedStyle.Render(nameRaw)
		case it.kind == kindGroup:
			nameStyled = groupNameStyle.Render(nameRaw)
		case it.kind == kindRoot:
			nameStyled = rootNameStyle.Render(nameRaw)
		default:
			nameStyled = nameRaw
		}

		branch := dimStyle.Render(row.prefix)

		b.WriteString(fmt.Sprintf("%s%s  %s  %s%s\n",
			cursor, check, aliasStyled, branch, nameStyled))
	}

	if len(rows) > maxV {
		above := m.offset
		below := len(rows) - end
		if above > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more\n", above)))
		}
		if below > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more\n", below)))
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑↓ navigate  space toggle  e edit alias  enter next  esc cancel"))
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
