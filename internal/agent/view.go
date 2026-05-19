package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

const HeaderCap = 9

func buildHeaderChips(workspaces []WorkspaceData) []Chip {
	var favs, recent []Chip
	for i := range workspaces {
		ws := &workspaces[i]
		for j := range ws.Projects {
			p := &ws.Projects[j]
			c := Chip{
				Kind:          KindProject,
				Name:          p.Name,
				Path:          p.Path,
				Favorite:      p.Favorite,
				LastActiveAt:  p.LastActiveAt,
				Project:       p,
				WorkspaceRoot: ws.Root,
			}
			if p.Favorite {
				favs = append(favs, c)
			} else if !p.LastActiveAt.IsZero() {
				recent = append(recent, c)
			}
		}
		for _, g := range ws.Groups {
			if !ws.FavoriteGroups[g] {
				continue
			}
			favs = append(favs, Chip{
				Kind:          KindGroup,
				Name:          g,
				Path:          GroupPath(ws.Root, g),
				Favorite:      true,
				WorkspaceRoot: ws.Root,
			})
		}
	}
	sortChipsByActivity(favs)
	sortChipsByActivity(recent)
	merged := append(favs, recent...)
	if len(merged) > HeaderCap {
		merged = merged[:HeaderCap]
	}
	return merged
}

func sortChipsByActivity(cs []Chip) {
	sort.Slice(cs, func(i, j int) bool {
		ai, aj := cs[i].LastActiveAt, cs[j].LastActiveAt
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return cs[i].Name < cs[j].Name
	})
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

func renderHeaderChips(chips []Chip, w, maxLines int) []string {
	if len(chips) == 0 || w <= 0 || maxLines <= 0 {
		return nil
	}
	tokens := make([]string, len(chips))
	for i, c := range chips {
		tokens[i] = formatChip(i+1, c)
	}
	return packChips(tokens, w, maxLines)
}

func formatChip(num int, c Chip) string {
	star := ""
	if c.Favorite {
		star = "*"
	}
	body := c.Name
	if c.Kind == KindGroup {
		body = "@" + c.Name
	}
	age := humanizeAge(c.LastActiveAt)
	if age == "" {
		return fmt.Sprintf("%s%d.%s", star, num, body)
	}
	return fmt.Sprintf("%s%d.%s %s", star, num, body, age)
}

func packChips(chips []string, w, maxLines int) []string {
	var lines []string
	cur := ""
	for _, c := range chips {
		next := c
		if cur != "" {
			next = cur + "  " + c
		}
		if tui.Width(next) > w {
			if cur != "" {
				lines = append(lines, cur)
				if len(lines) >= maxLines {
					return lines
				}
			}
			cur = c
			continue
		}
		cur = next
	}
	if cur != "" && len(lines) < maxLines {
		lines = append(lines, cur)
	}
	return lines
}

func styleHeaderLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = styleChipLine(line)
	}
	return out
}

func styleChipLine(line string) string {
	chips := strings.Split(line, "  ")
	for i, c := range chips {
		chips[i] = styleChip(c)
	}
	return strings.Join(chips, "  ")
}

func styleChip(c string) string {
	hasStar := strings.HasPrefix(c, "*")
	if hasStar {
		c = c[1:]
	}
	dot := strings.Index(c, ".")
	if dot < 0 {
		return c
	}
	num := c[:dot]
	rest := c[dot+1:]
	name, age, _ := strings.Cut(rest, " ")

	var b strings.Builder
	if hasStar {
		b.WriteString(favoriteStarStyle.Render("*"))
	}
	b.WriteString(chipNumberStyle.Render(num + "."))
	b.WriteString(chipNameStyle.Render(name))
	if age != "" {
		b.WriteString(" ")
		b.WriteString(activityAgeStyle.Render(age))
	}
	return b.String()
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
	groupStyle     = tui.Amber.Group
	itemStyle      = tui.Amber.Item
	dimStyle       = tui.Amber.Dim

	wtStyle       = tui.NewStyle().Foreground("108")
	sessionStyle  = tui.NewStyle().Foreground("110")
	badgeStyle    = tui.NewStyle().Foreground("240")
	wtStatusStyle = tui.NewStyle().Foreground("173")

	statusMsgStyle    = tui.NewStyle().Foreground("215").Bold(true)
	favoriteStarStyle = tui.NewStyle().Foreground("215")
	activityAgeStyle  = tui.NewStyle().Foreground("240")

	chipNumberStyle = tui.NewStyle().Foreground("245")
	chipNameStyle   = tui.NewStyle().Foreground("254").Bold(true)

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
	whichKeyKeyStyle    = tui.NewStyle().Foreground("215").Bold(true)
	whichKeyDescStyle   = tui.NewStyle().Foreground("245")
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

func (m *Model) updateChipAction(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	if m.chipTarget == nil {
		m.mode = viewList
		return m, nil
	}
	target := *m.chipTarget
	switch msg.String() {
	case "esc", "q":
		m.chipTarget = nil
		m.mode = viewList
		return m, nil
	case "c", "enter":
		m.Launch = &LaunchRequest{Cwd: target.Path}
		return m, tui.Quit
	case "s", "l":
		m.Launch = &LaunchRequest{Cwd: target.Path, ShellOnly: true}
		return m, tui.Quit
	case "p":
		m.pendingLaunch = &LaunchRequest{Cwd: target.Path}
		m.promptInput = ""
		m.chipTarget = nil
		m.mode = viewPromptInput
		return m, nil
	case "w":

		if target.Kind == KindProject && target.Project != nil {
			m.popupProj = target.Project
			m.wtBranch = ""
			m.wtField = 0
			m.wtNoLaunch = true
			m.chipTarget = nil
			m.mode = viewNewWorktree
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) viewChipAction() string {
	if m.chipTarget == nil {
		return m.viewList()
	}
	target := *m.chipTarget
	popupW := 44
	if m.width < 50 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	kindLabel := "project"
	if target.Kind == KindGroup {
		kindLabel = "group"
	}

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("Launch %s", kindLabel)))
	lines = append(lines, popupDimStyle.Width(innerW).Render(target.Name))
	lines = append(lines, popupDimStyle.Width(innerW).Render(target.Path))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  c / ⏎  claude"))
	lines = append(lines, popupItemStyle.Width(innerW).Render("  p     claude + prompt"))
	lines = append(lines, popupItemStyle.Width(innerW).Render("  s / l shell"))
	if target.Kind == KindProject {
		lines = append(lines, popupItemStyle.Width(innerW).Render("  w     new worktree"))
	}
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("  esc cancel"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}
