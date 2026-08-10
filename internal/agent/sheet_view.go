package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (s *sheet) pageRows(m *Model) int {
	chrome := 3
	if s.filterMode || s.filter.Value() != "" {
		chrome++
	}
	if s.statusMsg != "" {
		chrome++
	}
	if len(m.jobs) > 0 {
		chrome++
	}
	return max(1, m.height-chrome)
}

func (s *sheet) view(m *Model) string {
	panelW := max(1, m.width)
	var top []string

	position := fmt.Sprintf("%d/%d", min(s.cursor+1, len(s.visible)), len(s.visible))
	right := position
	if attention := m.activityAttentionToken(); attention != "" {
		right += " · " + attention
	}
	header := padPanelRight(" "+s.title(), right+" ", panelW)
	top = append(top, headerStyle.Width(panelW).Render(header))
	if s.filterMode || s.filter.Value() != "" {
		prompt := "/" + s.filter.Value()
		if s.filterMode {
			prompt = "/" + s.filter.View()
		}
		top = append(top, flashSearchStyle.Width(panelW).Render(tui.Truncate(" "+iconSearch+" "+prompt, panelW)))
	}

	footer := " Ctrl+O actions · / search · q back"
	if s.visual {
		footer = fmt.Sprintf(" %d selected · Ctrl+O actions · a archive · d delete · q cancel", len(s.visualWorktrees()))
	}
	bottom := []string{footerStyle.Width(panelW).Render(tui.Truncate(footer, panelW))}
	if strip := m.jobsStrip(); strip != "" {
		bottom = append([]string{statusMsgStyle.Width(panelW).Render(tui.Truncate(strip, panelW))}, bottom...)
	}
	status := s.statusMsg
	if status != "" {
		bottom = append([]string{statusMsgStyle.Width(panelW).Render(tui.Truncate(" "+presentLabel(status), panelW))}, bottom...)
	}

	bodyRows := max(0, m.height-len(top)-len(bottom))
	body := make([]string, 0, bodyRows)
	if len(s.visible) == 0 && bodyRows > 0 {
		empty := "(no matches)"
		if s.filter.Value() == "" {
			empty = "(empty)"
		}
		body = append(body, dimStyle.Width(panelW).Render(" "+empty))
	} else if bodyRows > 0 {
		start, end := tui.WindowAround(s.cursor, len(s.visible), bodyRows)
		for i := start; i < end; i++ {
			body = append(body, s.renderRow(i, panelW))
		}
	}
	for len(body) < bodyRows {
		body = append(body, strings.Repeat(" ", panelW))
	}

	rows := append(top, body...)
	rows = append(rows, bottom...)
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

func (s *sheet) renderRow(visIdx, width int) string {
	r := &s.rows[s.visible[visIdx]]
	if r.kind == rowHeader {
		left := fmt.Sprintf(" ── %s ", presentLabel(r.label))
		right := " " + formatSheetColumns(r.hint, r.activity)
		left = tui.Truncate(left, max(0, width-tui.Width(right)-1))
		gap := max(1, width-tui.Width(left)-tui.Width(right))
		text := left + strings.Repeat("─", gap) + right
		return dimStyle.Width(width).Render(text)
	}

	selected := visIdx == s.cursor
	visual := false
	if s.visual {
		start, end := s.visualAnchor, s.cursor
		if start > end {
			start, end = end, start
		}
		visual = visIdx >= start && visIdx <= end && r.kind == rowWorktree && r.wt != nil && !r.wt.IsMain
	}
	contentWidth := width
	if selected {
		contentWidth--
	}
	left := "  " + presentLabel(r.label)
	if r.indent > 0 {
		left = strings.Repeat("    ", r.indent) + presentLabel(r.label)
	}
	right := tui.Truncate(formatSheetColumns(r.hint, r.activity), max(0, contentWidth-1))
	line := padPanelRight(left, right, contentWidth)
	if selected {
		bar := accentBarStyle.Render("▌")
		return bar + selectedStyle.Width(contentWidth).Render(line)
	}
	if visual {
		return activityAgeStyle.Render("│") + selectedStyle.Width(width-1).Render(tui.Truncate(line, width-1))
	}
	return itemStyle.Width(width).Render(line)
}

func formatSheetColumns(status, activity string) string {
	status = presentLabel(status)
	activity = presentLabel(activity)
	if status == "" {
		return fmt.Sprintf("%8s", activity)
	}
	return fmt.Sprintf("%-10s %8s", status, activity)
}

func padPanelRight(left, right string, width int) string {
	right = tui.Truncate(right, max(0, width-1))
	left = tui.Truncate(left, max(0, width-tui.Width(right)-1))
	gap := width - tui.Width(left) - tui.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (s *sheet) breadcrumb() string {
	if s.mode == sheetGroup {
		return filepath.Base(s.workspaceRoot) + " › @" + presentLabel(s.group)
	}
	if s.target == nil {
		return "ws"
	}
	parent := filepath.Base(s.target.WorkspaceRoot)
	if s.target.Group != "" {
		parent = presentLabel(s.target.Group)
	}
	return parent + " › " + presentLabel(s.target.Name)
}

func (s *sheet) title() string {
	if s.mode == sheetGroup {
		return fmt.Sprintf("@%s", presentLabel(s.group))
	}
	if s.target != nil {
		return presentLabel(s.target.Name)
	}
	return "launch"
}

func (s *sheet) subtitle() string {
	if s.mode == sheetGroup {
		if s.groupPath != "" {
			return s.groupPath
		}
		return "group"
	}
	if s.target == nil {
		return ""
	}
	category := s.target.Category
	if category == "" {
		category = "personal"
	}
	return fmt.Sprintf("%s · %s", category, s.target.Path)
}

func (s *sheet) footerHints() (actions, nav string) {
	if s.filterMode {
		return "type to search  enter:results  Ctrl+C:cancel", "text editing keys remain active"
	}
	if s.mode == sheetGroup {
		actions = "⏎/l:open  s:shell  f:fav  a:archive-group  A:Activity  M:maint  /:search"
	} else {
		actions = "⏎/l:open  s:main  w:new  e:edit  f:fav  A:Activity  M:maint  /:search"
		if selected := len(s.visualWorktrees()); selected > 0 {
			actions = fmt.Sprintf("VISUAL %d  a:archive  d:delete  v/q:cancel", selected)
			return actions, "j/k:extend  g/G:first/last  ^d/^u:half  ^f/^b:page"
		}
		if row := s.focused(); row != nil && row.kind == rowWorktree && row.wt != nil && !row.wt.IsMain {
			actions = "⏎/l:open  v:select  a:archive  d:delete  " + actions[len("⏎/l:open  "):]
		}
	}
	return actions, "j/k:move  g/G:first/last  ^d/^u:half  ^f/^b:page  h:back  S:global"
}
