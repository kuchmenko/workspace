package agent

import (
	"fmt"
	"github.com/kuchmenko/workspace/internal/tui"
	"strings"
)

func (m *Model) renderListRows(listW int, dimAll bool) []string {
	var rows []string
	inFlash := m.mode == viewFlash

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	prevGroup := ""
	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

		curGroup := m.itemGroupKey(item)
		if prevGroup != "" && curGroup != prevGroup {
			rows = append(rows, strings.Repeat(" ", listW))
		}
		prevGroup = curGroup

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
		}

		rows = append(rows, line)
	}
	return rows
}

func (m *Model) itemGroupKey(item listItem) string {
	switch item.kind {
	case KindGroup:
		return "g:" + item.group
	case KindProject:
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
	indent := strings.Repeat("    ", item.indent)

	name := p.Name
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

	icon := DetectIcon(p.Path)
	left := fmt.Sprintf(" %s%s %s", indent, icon, name)

	var badgeParts []string
	if p.WorktreeCount > 1 {
		badgeParts = append(badgeParts, fmt.Sprintf("⚡%d", p.WorktreeCount))
	}
	if p.SessionCount > 0 {
		badgeParts = append(badgeParts, fmt.Sprintf("%ds", p.SessionCount))
	}
	badges := strings.Join(badgeParts, " · ")

	line := m.padRight(left, badges, w)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(line)
	}
	if selected {
		return m.renderSelected(line, itemStyle, w)
	}

	if badges != "" {
		leftPart := fmt.Sprintf(" %s%s %s", indent, icon, name)
		padding := w - tui.Width(leftPart) - tui.Width(badges) - 1
		if padding < 1 {
			padding = 1
		}
		return itemStyle.Render(leftPart) + strings.Repeat(" ", padding) + badgeStyle.Render(badges)
	}
	return itemStyle.Width(w).Render(line)
}

func (m *Model) renderWorktree(item listItem, selected bool, w int, dimAll bool, inFlash bool, isMatch bool, flashLabel rune) string {
	indent := strings.Repeat("    ", item.indent)
	name := item.group
	if name == "" {
		name = "worktree"
	}
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

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

	maxName := w - tui.Width(prefix) - tui.Width(status) - 2
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
		padding := w - tui.Width(left) - tui.Width(status) - 1
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
	indent := strings.Repeat("    ", item.indent)
	title := "(session)"
	if item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated), item.session.Title)
	}
	if inFlash && isMatch && item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated),
			flashInlineLabel(item.session.Title, m.flashQuery, flashLabel))
	}

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

func (m *Model) renderSelected(content string, base tui.Style, w int) string {
	bar := accentBarStyle.Render("▌")

	rest := selectedStyle.Width(w - 1).Render(content)
	return bar + rest
}

func (m *Model) padRight(left, right string, w int) string {
	lw := tui.Width(left)
	rw := tui.Width(right)
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

	chipLines := renderHeaderChips(m.headerChips, listW-2, 2)
	rows = append(rows, styleHeaderLines(chipLines)...)
	if len(chipLines) > 0 {
		rows = append(rows, strings.Repeat(" ", listW))
	}

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

	rows = append(rows, m.renderListRows(listW, false)...)

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

	panel := tui.JoinVertical(tui.Left, rows...)

	return tui.Place(
		m.width, m.height,
		tui.Center, tui.Center,
		panel,
	)
}
