package agent

import (
	"fmt"
	"path/filepath"
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

func (m *Model) renderGroup(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	arrow := "▸"
	if m.expanded[item.expandKey] {
		arrow = "▾"
	}
	name := presentLabel(item.group)
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery.Value(), flashLabel)
	}
	label := fmt.Sprintf("   %s %s", arrow, name)
	label = tui.Truncate(label, w)

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

	name := presentLabel(p.Name)
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery.Value(), flashLabel)
	}

	icon := DetectIcon(p.Path)
	left := fmt.Sprintf(" %s%s %s", indent, icon, name)

	var badgeParts []string
	if p.WorktreeCount > 1 {
		badgeParts = append(badgeParts, fmt.Sprintf("⚡%d", p.WorktreeCount))
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
		leftPart := tui.Truncate(fmt.Sprintf(" %s%s %s", indent, icon, name), max(0, w-tui.Width(badges)-1))
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

	rest := selectedStyle.Width(w - 1).Render(tui.Truncate(content, w-1))
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
	listW := explorerPanelWidth(m.width)

	var rows []string

	chipLines := renderHeaderChips(m.headerChips, max(1, listW-2), 2)
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
		searchLine := fmt.Sprintf(" %s %s", prefix, m.flashQuery.View())
		searchLine = tui.Truncate(searchLine, listW)
		rows = append(rows, flashSearchStyle.Width(listW).Render(searchLine))
	} else {
		bc := m.breadcrumb()
		pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
		hdr := m.padRight(tui.Truncate(" "+bc, max(0, listW-tui.Width(pos)-2)), pos+" ", listW)
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
		hint := "letter to jump · esc cancel"
		footer := m.padRight(tui.Truncate(matchInfo, max(0, listW-tui.Width(hint)-2)), tui.Truncate(hint+" ", listW), listW)
		rows = append(rows, footerStyle.Width(listW).Render(footer))
	} else {
		actions, nav := m.footerHints()
		rows = append(rows, footerStyle.Width(listW).Render(tui.Truncate(" "+actions, listW)))
		rows = append(rows, footerStyle.Width(listW).Render(tui.Truncate(" "+nav, listW)))
	}

	panel := tui.JoinVertical(tui.Left, rows...)

	return tui.Place(
		m.width, m.height,
		tui.Center, tui.Center,
		panel,
	)
}

func explorerPanelWidth(width int) int {
	if width < 102 {
		return max(1, width-6)
	}
	return 96
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
				Path:          filepath.Join(ws.Root, g),
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
		tokens[i] = tui.Truncate(formatChip(i+1, c), w)
	}
	return packChips(tokens, w, maxLines)
}

func formatChip(num int, c Chip) string {
	star := ""
	if c.Favorite {
		star = "*"
	}
	body := presentLabel(c.Name)
	if c.Kind == KindGroup {
		body = "@" + body
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

	badgeStyle = tui.NewStyle().Foreground("240")

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
