package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) viewLifecycle() string {
	panelW := max(1, m.width)
	var top []string

	header := padPanelRight(" Lifecycle › "+m.lifecycleScopeLabel(), m.lifecyclePhaseLabel()+" ", panelW)
	top = append(top, headerStyle.Width(panelW).Render(header))
	top = append(top, dimStyle.Width(panelW).Render(tui.Truncate(" Safe review before workspace mutation", panelW)))

	body := m.lifecycleBody()
	actions, nav := m.lifecycleFooterHints()
	bottom := []string{
		footerStyle.Width(panelW).Render(tui.Truncate(" "+actions, panelW)),
		footerStyle.Width(panelW).Render(tui.Truncate(" "+nav, panelW)),
	}
	bodyRows := m.lifecycleBodyRows()
	start := min(m.lifecycle.scroll, max(0, len(body)-bodyRows))
	rows := append([]string(nil), top...)
	for i := 0; i < bodyRows; i++ {
		line := ""
		if start+i < len(body) {
			line = body[start+i]
		}
		style := itemStyle
		if strings.HasPrefix(line, "Error: ") {
			style = statusMsgStyle
		}
		rows = append(rows, style.Width(panelW).Render(tui.Truncate(" "+line, panelW)))
	}
	rows = append(rows, bottom...)
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

func (m *Model) lifecycleBodyRows() int {
	topRows := 2
	return max(0, m.height-topRows-2)
}

func (m *Model) lifecycleBody() []string {
	return m.lifecycleBodyFor(m.lifecycle)
}

func (m *Model) lifecycleBodyFor(lm *lifecycleModel) []string {
	var lines []string
	switch lm.phase {
	case lifecycleSelect:
		lines = append(lines, "", "1 / a  Archive projects", "2 / w  Archive old worktrees")
	case lifecycleThreshold:
		lines = append(lines, "", "Age threshold (h/d/w/month)", lm.input+"█")
	case lifecyclePlanning:
		lines = append(lines, "", "Building archive plan in background…")
		if m.debugLogPath != "" {
			lines = append(lines, "Log: "+m.debugLogPath)
		}
	case lifecycleReview:
		lines = append(lines, "", lm.message)
		if lm.scope.kind == lifecycleWorktree && len(lm.scope.worktrees) > 1 {
			lines = append(lines, "")
			for _, wt := range lm.scope.worktrees {
				state := ""
				if wt.Dirty {
					state = " · modified"
				}
				lines = append(lines, "• "+worktreeDisplayName(*wt)+state)
			}
		}
		if lm.action == lifecycleArchiveOldWorktrees {
			lines = append(lines, fmt.Sprintf("eligible %d · recent %d · main %d · modified %d · protected %d · unpushed %d", len(lm.plan.Eligible), lm.plan.Recent, lm.plan.Main, lm.plan.Dirty, lm.plan.Protected, lm.plan.Unpushed))
		}
	case lifecycleRunning:
		percent := 0
		if lm.total > 0 {
			percent = lm.completed * 100 / lm.total
		}
		lines = append(lines, "", fmt.Sprintf("Progress %d/%d · %d%% · %s", lm.completed, lm.total, percent, time.Since(lm.startedAt).Round(time.Second)))
		if lm.current != "" {
			lines = append(lines, "Current: "+lm.current)
		}
		if m.debugLogPath != "" {
			lines = append(lines, "Log: "+m.debugLogPath)
		}
		if len(lm.details) > 0 {
			lines = append(lines, "")
			lines = append(lines, lm.details...)
		}
	case lifecycleRefreshing:
		lines = append(lines, "", "Applying refreshed workspace state in background…")
		if m.debugLogPath != "" {
			lines = append(lines, "Log: "+m.debugLogPath)
		}
	case lifecycleResult:
		lines = append(lines, "", lm.message)
		if len(lm.details) > 0 {
			lines = append(lines, "")
			lines = append(lines, lm.details...)
		}
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
	case lifecyclePlanning:
		actions = "planning in background"
	case lifecycleReview:
		actions = "enter/y:confirm  n:cancel"
	case lifecycleRunning:
		actions = "running in background"
	case lifecycleRefreshing:
		actions = "refreshing in background"
	case lifecycleResult:
		actions = "q:close"
	}
	nav = "q:back"
	if m.lifecycle.phase == lifecycleReview || m.lifecycle.phase == lifecycleRunning || m.lifecycle.phase == lifecycleRefreshing || m.lifecycle.phase == lifecycleResult {
		nav = "j/k:scroll  g/G:first/last  ^d/^u:half  q:back"
	}
	return actions, nav
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
		if len(lm.scope.worktrees) > 1 {
			return fmt.Sprintf("%d selected worktrees", len(lm.scope.worktrees))
		}
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
	case lifecyclePlanning:
		return "planning"
	case lifecycleReview:
		return "review"
	case lifecycleRunning:
		return "running"
	case lifecycleRefreshing:
		return "refreshing"
	case lifecycleResult:
		return "result"
	default:
		return ""
	}
}
