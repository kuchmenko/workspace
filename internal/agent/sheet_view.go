package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (s *sheet) view(width, height int) string {
	popupW := 60
	if width < 66 {
		popupW = width - 6
	}
	if popupW < 30 {
		popupW = 30
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(s.title()))
	if sub := s.subtitle(); sub != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render(sub))
	}

	if s.filterMode || s.filter.Value() != "" {
		prompt := "/" + s.filter.Value()
		if s.filterMode {
			prompt = "/" + s.filter.View()
		}
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(" "+prompt))
	}
	lines = append(lines, "")

	if len(s.visible) == 0 {
		empty := "(no matches)"
		if s.filter.Value() == "" {
			empty = "(empty)"
		}
		lines = append(lines, popupDimStyle.Width(innerW).Render(empty))
	} else {
		const maxRows = 18
		start, end := tui.WindowAround(s.cursor, len(s.visible), maxRows)
		for i := start; i < end; i++ {
			lines = append(lines, s.renderRow(i, innerW))
		}
		if start > 0 {
			lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("  …%d above", start)))
		}
		if end < len(s.visible) {
			lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("  …%d below", len(s.visible)-end)))
		}
	}

	if s.statusMsg != "" {
		lines = append(lines, "")
		lines = append(lines, statusMsgStyle.Width(innerW).Render(" "+presentLabel(s.statusMsg)))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render(s.footerHint()))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return tui.Place(width, height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}

func (s *sheet) renderRow(visIdx, innerW int) string {
	r := &s.rows[s.visible[visIdx]]
	selected := visIdx == s.cursor

	if r.kind == rowHeader {
		text := fmt.Sprintf("── %s ", r.label)
		if len(text) < innerW {
			text += strings.Repeat("─", innerW-len(text))
		}
		return popupDimStyle.Width(innerW).Render(text)
	}

	label := presentLabel(r.label)
	hint := presentLabel(r.hint)
	key := r.keyHint

	left := "  " + label
	if r.indent > 0 {
		left = strings.Repeat("    ", r.indent) + label
	}

	right := ""
	if hint != "" {
		right += hint
	}
	if key != "" {
		if right != "" {
			right += "  "
		}
		right += key
	}

	leftW := tui.Width(left)
	rightW := tui.Width(right)
	gap := innerW - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right

	if selected {
		return popupSelectedStyle.Width(innerW).Render(line)
	}
	return popupItemStyle.Width(innerW).Render(line)
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
	cat := s.target.Category
	if cat == "" {
		cat = "personal"
	}
	return fmt.Sprintf("%s · %s", cat, s.target.Path)
}

func (s *sheet) footerHint() string {
	if s.filterMode {
		return "  type to filter · enter:apply · esc:clear"
	}
	if s.mode == sheetGroup {
		return "  ⏎:open  /:filter  esc:back"
	}
	r := s.focused()
	if r == nil {
		return "  /:filter  esc:back"
	}
	switch r.kind {
	case rowWorktree:
		if r.wt != nil && !r.wt.IsMain {
			return "  ⏎:shell  d:delete"
		}
		return "  ⏎:shell"
	case rowAction:
		return "  ⏎:run  /:filter  esc:back"
	}
	return "  /:filter  esc:back"
}
