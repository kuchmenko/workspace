package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) renderListRows(listW int, dimAll bool) []string {
	var rows []string
	inFlash := m.mode == viewFlash

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

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
		case KindWorkspace:
			line = m.renderWorkspace(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindGroup:
			line = m.renderGroup(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindProject:
			line = m.renderProject(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindWorktree:
			project := *item.parentProj
			project.Name = item.group
			line = m.renderProject(listItem{project: &project}, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		}

		rows = append(rows, line)
	}
	return rows
}

func (m *Model) renderWorkspace(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	arrow := "▸"
	if m.expanded[item.expandKey] {
		arrow = "▾"
	}
	name := presentLabel(item.workspaceName)
	if name == "" {
		name = presentLabel(filepath.Base(item.workspaceRoot))
	}
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery.Value(), flashLabel)
	}
	label := fmt.Sprintf("  %s %s", arrow, name)
	badges := []string{}
	if alias := m.aliasForTarget(item.workspaceRoot, workspaceAliasRootTarget); alias != "" {
		badges = append(badges, "alias "+alias)
	}
	badges = append(badges, projectCountLabel(item.count))
	label = m.padRight(label, strings.Join(badges, " · "), w)
	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, workspaceStyle, w)
	}
	return workspaceStyle.Width(w).Render(label)
}

func (m *Model) renderGroup(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	if item.projectionGroup && item.expandKey == recentKey() {
		return strings.Repeat(" ", w)
	}
	arrow := "▸"
	if m.expanded[item.expandKey] {
		arrow = "▾"
	}
	name := presentLabel(item.group)
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery.Value(), flashLabel)
	}
	label := fmt.Sprintf("%s%s %s", strings.Repeat("  ", item.indent+1), arrow, name)
	var badgeParts []string
	if !item.projectionGroup {
		if alias := m.aliasForTarget(item.workspaceRoot, item.group); alias != "" {
			badgeParts = append(badgeParts, "alias "+alias)
		}
		if badge := m.runnerBadge(m.groupRunnerTarget(item.workspaceRoot, item.group), item.path); badge != "" {
			badgeParts = append(badgeParts, badge)
		}
		badgeParts = append(badgeParts, projectCountLabel(item.count))
	}
	badge := strings.Join(badgeParts, " · ")
	label = m.padRight(label, badge, w)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, groupStyle, w)
	}
	return groupStyle.Width(w).Render(label)
}

func projectCountLabel(count int) string {
	if count == 1 {
		return "1 project"
	}
	return fmt.Sprintf("%d projects", count)
}

func (m *Model) renderProject(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	p := item.project
	name := presentLabel(p.Name)
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery.Value(), flashLabel)
	}

	left := strings.Repeat("  ", item.indent+1) + name
	if item.indent > 0 {
		left = "  " + left
	}

	var badgeParts []string
	if alias := m.aliasForTarget(p.WorkspaceRoot, p.ID); alias != "" {
		badgeParts = append(badgeParts, "alias "+alias)
	}
	if badge := m.runnerBadge(m.projectRunnerTarget(p), p.Path); badge != "" {
		badgeParts = append(badgeParts, badge)
	}
	if p.WorktreeCount > 1 {
		badgeParts = append(badgeParts, fmt.Sprintf("%d worktrees", p.WorktreeCount))
	}
	if age := humanizeAge(p.LastActiveAt); age != "" {
		badgeParts = append(badgeParts, age)
	}
	badges := strings.Join(badgeParts, " · ")
	left = tui.Truncate(left, max(0, w-tui.Width(badges)-1))
	line := m.padRight(left, badges, w)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(line)
	}
	if selected {
		return m.renderSelected(line, itemStyle, w)
	}

	if badges != "" {
		leftPart := tui.Truncate(left, max(0, w-tui.Width(badges)-1))
		padding := w - tui.Width(leftPart) - tui.Width(badges) - 1
		if padding < 1 {
			padding = 1
		}
		return itemStyle.Render(leftPart) + strings.Repeat(" ", padding) + badgeStyle.Render(badges)
	}
	return itemStyle.Width(w).Render(line)
}
func (m *Model) renderSelected(content string, base tui.Style, w int) string {
	bar := accentBarStyle.Render("▌")
	rest := ""
	if len(content) > 0 {
		rest = content[1:]
	}
	rest = selectedStyle.Width(w - 1).Render(tui.Truncate(rest, w-1))
	return bar + rest
}

func (m *Model) padRight(left, right string, w int) string {
	right = tui.Truncate(right, max(0, w-1))
	left = tui.Truncate(left, max(0, w-tui.Width(right)-1))
	lw := tui.Width(left)
	rw := tui.Width(right)
	gap := w - lw - rw - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) viewList() string {
	listW := max(1, m.width)
	var rows []string

	inFlash := m.mode == viewFlash
	if inFlash {
		prefix := iconSearch
		if m.flashGlobal {
			prefix = iconSearch + " all"
		}
		searchLine := fmt.Sprintf(" %s %s", prefix, m.flashQuery.View())
		searchLine = tui.Truncate(searchLine, listW)
		rows = append(rows, flashSearchStyle.Width(listW).Render(searchLine))
	} else {
		position := 0
		if len(m.items) > 0 {
			position = m.cursor + 1
		}
		pos := fmt.Sprintf("%d/%d", position, len(m.items))
		right := pos
		if attention := m.activityAttentionToken(); attention != "" {
			right += " · " + attention
		}
		hdr := m.padRight(" "+m.homeTitle(), right+" ", listW)
		rows = append(rows, headerStyle.Width(listW).Render(hdr))
	}

	rows = append(rows, m.renderListRows(listW, false)...)
	if strip := m.jobsStrip(); strip != "" && !inFlash {
		rows = append(rows, statusMsgStyle.Width(listW).Render(tui.Truncate(strip, listW)))
	}
	status := m.statusMsg
	if status != "" && !inFlash {
		rows = append(rows, statusMsgStyle.Width(listW).Render(tui.Truncate(" "+presentLabel(status), listW)))
	} else if inFlash {
		matchInfo := fmt.Sprintf(" %d matches", len(m.flashMatches))
		hint := "Enter results · Ctrl+C cancel"
		footer := m.padRight(tui.Truncate(matchInfo, max(0, listW-tui.Width(hint)-2)), tui.Truncate(hint+" ", listW), listW)
		rows = append(rows, footerStyle.Width(listW).Render(footer))
	} else {
		footer := " Ctrl+O commands · a alias · / search · R runners · S search all · q quit"
		rows = append(rows, footerStyle.Width(listW).Render(tui.Truncate(footer, listW)))
	}
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

func (m *Model) homeTitle() string {
	switch m.homeView {
	case config.ExplorerViewProjects:
		return "Projects"
	default:
		if m.recentOrder == config.RecentOrderAsc {
			return "Recent · oldest first"
		}
		return "Recent · newest first"
	}
}

func humanizeAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return humanizeAgeAt(t, time.Now())
}

func humanizeAgeAt(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return formatInt(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return formatInt(int(d.Hours())) + "h"
	case d < 48*time.Hour:
		return "yday"
	case d < 7*24*time.Hour:
		return formatInt(int(d.Hours()/24)) + "d"
	case d < 30*24*time.Hour:
		return formatInt(int(d.Hours()/(24*7))) + "w"
	case d < 365*24*time.Hour:
		return formatInt(int(d.Hours()/(24*30))) + "mo"
	default:
		return formatInt(int(d.Hours()/(24*365))) + "y"
	}
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

var (
	headerStyle    = tui.Amber.Header
	footerStyle    = tui.Amber.Footer
	accentBarStyle = tui.NewStyle().Foreground("215")
	selectedStyle  = tui.Amber.Selected
	workspaceStyle = tui.NewStyle().Foreground("215").Bold(true)
	groupStyle     = tui.Amber.Group
	itemStyle      = tui.Amber.Item
	dimStyle       = tui.Amber.Dim

	badgeStyle = tui.NewStyle().Foreground("240")

	statusMsgStyle   = tui.NewStyle().Foreground("215").Bold(true)
	activityAgeStyle = tui.NewStyle().Foreground("240")

	flashSearchStyle = tui.NewStyle().Bold(true).Foreground("215").Background("235")
	flashLabelStyle  = tui.NewStyle().Bold(true).Foreground("235").Background("215")
	flashMatchStyle  = tui.NewStyle().Underline(true).Foreground("215")

	popupBorderStyle   = tui.NewStyle().Border(tui.RoundedBorder()).BorderForeground("173").Padding(1, 1)
	popupTitleStyle    = tui.NewStyle().Bold(true).Foreground("215")
	popupSelectedStyle = tui.NewStyle().Bold(true).Foreground("215").Background("237")
	popupItemStyle     = tui.NewStyle().Foreground("254")
	popupDimStyle      = tui.NewStyle().Foreground("240")

	whichKeyBorderStyle = tui.NewStyle().Border(tui.RoundedBorder()).BorderForeground("173").Padding(0, 1)
	whichKeyTitleStyle  = tui.NewStyle().Foreground("215").Bold(true)
)

func flashInlineLabel(name, query string, label rune) string {
	if query == "" {
		return name
	}
	runes := []rune(name)
	lower := []rune(strings.ToLower(name))
	q := []rune(strings.ToLower(query))
	idx := -1
	for i := 0; i+len(q) <= len(lower); i++ {
		if string(lower[i:i+len(q)]) == string(q) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return name
	}
	matchEnd := idx + len(q)

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
