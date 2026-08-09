package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) viewLifecycle() string {
	panelW := explorerPanelWidth(m.width)
	chipLines := renderHeaderChips(m.headerChips, max(1, panelW-2), 2)
	var top []string
	top = append(top, styleHeaderLines(chipLines)...)
	if len(chipLines) > 0 {
		top = append(top, strings.Repeat(" ", panelW))
	}

	header := padPanelRight(" Lifecycle › "+m.lifecycleScopeLabel(), m.lifecyclePhaseLabel()+" ", panelW)
	top = append(top, headerStyle.Width(panelW).Render(header))
	top = append(top, dimStyle.Width(panelW).Render(tui.Truncate(" Safe review before workspace mutation", panelW)))

	body := m.lifecycleBody()
	actions, nav := m.lifecycleFooterHints()
	bottom := []string{
		footerStyle.Width(panelW).Render(tui.Truncate(" "+actions, panelW)),
		footerStyle.Width(panelW).Render(tui.Truncate(" "+nav, panelW)),
	}
	bodyRows := max(0, m.height-len(top)-len(bottom))
	rows := append([]string(nil), top...)
	for i := 0; i < bodyRows; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		style := itemStyle
		if strings.HasPrefix(line, "Error: ") {
			style = statusMsgStyle
		}
		rows = append(rows, style.Width(panelW).Render(tui.Truncate(" "+line, panelW)))
	}
	rows = append(rows, bottom...)
	return tui.Place(m.width, m.height, tui.Center, tui.Center, tui.JoinVertical(tui.Left, rows...))
}

func (m *Model) lifecycleBody() []string {
	lm := m.lifecycle
	var lines []string
	switch lm.phase {
	case lifecycleSelect:
		lines = append(lines, "", "1 / a  Archive projects", "2 / w  Archive old worktrees")
	case lifecycleThreshold:
		lines = append(lines, "", "Age threshold (h/d/w/month)", lm.input+"█")
	case lifecycleReview:
		lines = append(lines, "", lm.message)
		if lm.action == lifecycleArchiveOldWorktrees {
			lines = append(lines, fmt.Sprintf("eligible %d · recent %d · main %d · dirty %d · protected %d · unpushed %d", len(lm.plan.Eligible), lm.plan.Recent, lm.plan.Main, lm.plan.Dirty, lm.plan.Protected, lm.plan.Unpushed))
		}
	case lifecycleResult:
		lines = append(lines, "", lm.message)
	}
	if lm.errorText != "" {
		lines = append(lines, "", "Error: "+lm.errorText)
	}
	return lines
}

func (m *Model) lifecycleFooterHints() (actions, nav string) {
	switch m.lifecycle.phase {
	case lifecycleSelect:
		actions = "1/a:archive projects  2/w:archive old worktrees"
	case lifecycleThreshold:
		actions = "type age threshold  enter:review"
	case lifecycleReview:
		actions = "y:confirm  n:cancel"
	case lifecycleResult:
		actions = "esc:close"
	}
	return actions, "esc:back"
}

func (m *Model) lifecycleScopeLabel() string {
	lm := m.lifecycle
	switch lm.scope.kind {
	case lifecycleProject:
		if lm.scope.project != nil {
			return presentLabel(lm.scope.project.Name)
		}
		return "project"
	case lifecycleGroup:
		return "@" + presentLabel(lm.scope.group)
	case lifecycleWorktree:
		if lm.scope.worktree != nil {
			return worktreeDisplayName(*lm.scope.worktree)
		}
		return "worktree"
	default:
		return "all workspaces"
	}
}

func (m *Model) lifecyclePhaseLabel() string {
	switch m.lifecycle.phase {
	case lifecycleSelect:
		return "choose"
	case lifecycleThreshold:
		return "threshold"
	case lifecycleReview:
		return "review"
	case lifecycleResult:
		return "result"
	default:
		return ""
	}
}
