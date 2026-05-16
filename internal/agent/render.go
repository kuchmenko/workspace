package agent

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) renderListRows(listW int, dimAll bool) []string {
	var rows []string
	inFlash := m.mode == viewFlash

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	// Track group boundaries for visual spacing.
	prevGroup := ""
	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

		// Inject empty line between groups.
		curGroup := m.itemGroupKey(item)
		if prevGroup != "" && curGroup != prevGroup {
			rows = append(rows, strings.Repeat(" ", listW))
		}
		prevGroup = curGroup

		// In flash mode: check if this item is in the match set.
		isMatch := false
		flashLabel := rune(0)
		if inFlash {
			for mi, idx := range m.flashMatches {
				if idx == i {
					isMatch = true
					if mi < len(m.flashLabels) {
						flashLabel = m.flashLabels[mi]
					}
					break
				}
			}
		}

		var line string
		switch item.kind {
		case KindGroup:
			line = m.renderGroup(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindProject:
			line = m.renderProject(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindWorktree:
			line = m.renderWorktree(item, selected, listW, dimAll, inFlash, isMatch, flashLabel)
		case KindPortal:
			line = m.renderSession(item, selected, listW, dimAll, inFlash, isMatch, flashLabel)
		case KindSection:
			line = m.renderSection(item, listW, dimAll)
		}

		rows = append(rows, line)
	}
	return rows
}

// itemGroupKey returns a key that identifies the visual group boundary
// for inserting blank lines between groups. KindSection rows return
// their own key per title so each section visually owns its block
// (Favorites != Recent != divider); the rows beneath them inherit the
// tree's normal keys because the shortcut projects are inHeader=true
// without any Group of their own.
func (m *Model) itemGroupKey(item listItem) string {
	switch item.kind {
	case KindSection:
		return "section:" + item.sectionTitle
	case KindGroup:
		return "g:" + item.group
	case KindProject:
		if item.inHeader {
			return "header"
		}
		if item.project.Group != "" {
			return "g:" + item.project.Group
		}
		return "ungrouped"
	case KindWorktree, KindPortal:
		if item.parentProj != nil && item.parentProj.Group != "" {
			return "g:" + item.parentProj.Group
		}
		return "ungrouped"
	}
	return ""
}

// renderSection draws the non-selectable header rows: section titles
// ("Favorites" / "Recent"), the divider line above the full tree, and
// the empty-state hint shown inside an empty Favorites view. All four
// share one style block; the title text already disambiguates them.
func (m *Model) renderSection(item listItem, w int, dimAll bool) string {
	text := item.sectionTitle
	if text == "" {
		return strings.Repeat(" ", w)
	}
	label := "  " + text
	if dimAll {
		return dimStyle.Width(w).Render(label)
	}
	return sectionStyle.Width(w).Render(label)
}

func (m *Model) renderGroup(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	arrow := "▸"
	if m.expanded[item.group] {
		arrow = "▾"
	}
	name := item.group
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}
	label := fmt.Sprintf("   %s %s", arrow, name)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, groupStyle, w)
	}
	return groupStyle.Width(w).Render(label)
}

func (m *Model) renderProject(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	p := item.project
	indent := strings.Repeat("  ", item.indent)

	// Project rows in the Favorites/Recent shortcut section have a
	// distinct shape: a leading `*` marker for favorites, no
	// expansion arrow (they never expand to worktrees here), and a
	// trailing age column ("2m linux"). The tree variant below is
	// unchanged.
	if item.inHeader {
		return m.renderHeaderProject(p, selected, inFlash, isMatch, flashLabel, w, dimAll)
	}

	expandMark := ""
	if p.WorktreeCount > 1 || p.SessionCount > 0 {
		if m.expanded["proj:"+p.ID] {
			expandMark = "▾ "
		} else {
			expandMark = "▸ "
		}
	}

	name := p.Name
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

	// Build left part: indent + expand + icon + name
	left := fmt.Sprintf(" %s%s%s %s", indent, expandMark, iconProject, name)

	// Build right part: badges (right-aligned)
	var badgeParts []string
	if p.WorktreeCount > 1 {
		badgeParts = append(badgeParts, fmt.Sprintf("%dwt", p.WorktreeCount))
	}
	if p.SessionCount > 0 {
		badgeParts = append(badgeParts, fmt.Sprintf("%ds", p.SessionCount))
	}
	badges := strings.Join(badgeParts, " · ")

	// Pad between left and right to fill width.
	line := m.padRight(left, badges, w)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(line)
	}
	if selected {
		return m.renderSelected(line, itemStyle, w)
	}
	// Render with styled badges.
	if badges != "" {
		leftPart := fmt.Sprintf(" %s%s%s %s", indent, expandMark, iconProject, name)
		padding := w - lipgloss.Width(leftPart) - lipgloss.Width(badges) - 1
		if padding < 1 {
			padding = 1
		}
		return itemStyle.Render(leftPart) + strings.Repeat(" ", padding) + badgeStyle.Render(badges)
	}
	return itemStyle.Width(w).Render(line)
}

// renderHeaderProject draws a project row inside the Favorites/Recent
// shortcut section: `*` star for favorites, project icon, name, and a
// right-aligned `2m linux` activity column. The row is fully selectable
// and shares Enter/p/l semantics with tree rows — only the visual shape
// differs.
func (m *Model) renderHeaderProject(p *Project, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	name := p.Name
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

	star := "  "
	if p.Favorite {
		star = "* "
	}
	left := fmt.Sprintf("  %s%s %s", star, iconProject, name)

	var rightParts []string
	if age := humanizeAge(p.LastActiveAt); age != "" {
		rightParts = append(rightParts, age)
	}
	if p.LastActiveMachine != "" {
		rightParts = append(rightParts, p.LastActiveMachine)
	}
	right := strings.Join(rightParts, " ")

	line := m.padRight(left, right, w)
	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(line)
	}
	if selected {
		return m.renderSelected(line, itemStyle, w)
	}
	if right == "" {
		// Star + project body share the project color; favorited
		// projects get a brighter star via favoriteStarStyle for visual
		// scanability without using a different background.
		if p.Favorite {
			body := fmt.Sprintf("  %s %s", iconProject, name)
			return favoriteStarStyle.Render("  * ") + itemStyle.Render(body[len("  * "):]) + strings.Repeat(" ", w-lipgloss.Width(left))
		}
		return itemStyle.Width(w).Render(line)
	}
	padding := w - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if padding < 1 {
		padding = 1
	}
	leftRendered := itemStyle.Render(left)
	if p.Favorite {
		// Overlay just the star with the brighter style. left already
		// contains the star at positions 2-3 ("  * "+icon+...).
		leftRendered = itemStyle.Render("  ") +
			favoriteStarStyle.Render("* ") +
			itemStyle.Render(left[len("  * "):])
	}
	return leftRendered + strings.Repeat(" ", padding) + activityAgeStyle.Render(right) + " "
}

func (m *Model) renderWorktree(item listItem, selected bool, w int, dimAll bool, inFlash bool, isMatch bool, flashLabel rune) string {
	indent := strings.Repeat("  ", item.indent)
	name := item.group // worktreeDisplayName stored in group field
	if name == "" {
		name = "worktree"
	}
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

	// Status indicators: * for dirty, ↑N for ahead.
	var status string
	if item.worktree != nil {
		if item.worktree.Dirty {
			status += "*"
		}
		if item.worktree.Ahead > 0 {
			status += fmt.Sprintf(" ↑%d", item.worktree.Ahead)
		}
		status = strings.TrimSpace(status)
	}

	prefix := fmt.Sprintf(" %s%s ", indent, iconWorktree)
	// Truncate name to fit available width.
	maxName := w - lipgloss.Width(prefix) - lipgloss.Width(status) - 2
	if maxName > 0 && !inFlash {
		name = truncateStr(name, maxName)
	}

	left := prefix + name
	if status != "" {
		line := m.padRight(left, status+" ", w)
		if dimAll || (inFlash && !isMatch) {
			return dimStyle.Width(w).Render(line)
		}
		if selected {
			return m.renderSelected(line, wtStyle, w)
		}
		leftRendered := wtStyle.Render(left)
		padding := w - lipgloss.Width(left) - lipgloss.Width(status) - 1
		if padding < 1 {
			padding = 1
		}
		return leftRendered + strings.Repeat(" ", padding) + wtStatusStyle.Render(status)
	}

	label := left
	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, wtStyle, w)
	}
	return wtStyle.Width(w).Render(label)
}

func (m *Model) renderSession(item listItem, selected bool, w int, dimAll bool, inFlash bool, isMatch bool, flashLabel rune) string {
	indent := strings.Repeat("  ", item.indent)
	title := "(session)"
	if item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated), item.session.Title)
	}
	if inFlash && isMatch && item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated),
			flashInlineLabel(item.session.Title, m.flashQuery, flashLabel))
	}

	// Truncate to prevent multiline wrapping.
	prefix := fmt.Sprintf(" %s%s ", indent, iconSession)
	maxTitle := w - len([]rune(prefix)) - 1
	if maxTitle > 0 {
		title = truncateStr(title, maxTitle)
	}
	label := prefix + title

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, sessionStyle, w)
	}
	return sessionStyle.Width(w).Render(label)
}

// truncateStr truncates a string to maxLen runes, adding … if needed.
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

// renderSelected renders a line with the amber ▌ selection bar.
func (m *Model) renderSelected(content string, base lipgloss.Style, w int) string {
	bar := accentBarStyle.Render("▌")
	// Render content with selected style, leave room for the bar.
	rest := selectedStyle.Width(w - 1).Render(content)
	return bar + rest
}

// padRight fills space between left content and right badges.
func (m *Model) padRight(left, right string, w int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := w - lw - rw - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) viewList() string {
	listW := 60
	if m.width > 80 {
		listW = 70
	}
	if m.width < 66 {
		listW = m.width - 6
	}

	var rows []string

	// Header — breadcrumb + position.
	inFlash := m.mode == viewFlash
	if inFlash {
		prefix := iconSearch
		if m.flashGlobal {
			prefix = iconSearch + " all"
		}
		searchLine := fmt.Sprintf(" %s %s█", prefix, m.flashQuery)
		rows = append(rows, flashSearchStyle.Width(listW).Render(searchLine))
	} else {
		bc := m.breadcrumb()
		pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
		hdr := m.padRight(" "+bc, pos+" ", listW)
		rows = append(rows, headerStyle.Width(listW).Render(hdr))
	}

	// List items.
	rows = append(rows, m.renderListRows(listW, false)...)

	// Footer — status message or context-sensitive hints.
	if m.statusMsg != "" && !inFlash {
		rows = append(rows, statusMsgStyle.Width(listW).Render(" "+m.statusMsg))
	} else if inFlash {
		matchInfo := fmt.Sprintf(" %d matches", len(m.flashMatches))
		hint := "letter to jump · esc cancel"
		footer := m.padRight(matchInfo, hint+" ", listW)
		rows = append(rows, footerStyle.Width(listW).Render(footer))
	} else {
		actions, nav := m.footerHints()
		rows = append(rows, footerStyle.Width(listW).Render(" "+actions))
		rows = append(rows, footerStyle.Width(listW).Render(" "+nav))
	}

	panel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}
