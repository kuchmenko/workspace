package cli

import (
	"fmt"
	"strings"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/git"
	workspacesync "codeberg.org/kuchmenko/workspace/internal/sync"
	"codeberg.org/kuchmenko/workspace/internal/tui"
)

var (
	syncTitleStyle    = tui.NewStyle().Bold(true).Foreground("6")
	syncActiveStyle   = tui.NewStyle().Foreground("2")
	syncFailedStyle   = tui.NewStyle().Foreground("1")
	syncDimStyle      = tui.NewStyle().Foreground("8")
	syncSelectedStyle = tui.NewStyle().Foreground("6").Bold(true)
)

func (m syncModel) View() string {
	switch m.stage {
	case syncProbing:
		return m.viewProbing(false)
	case syncReview:
		return m.viewReview()
	case syncConfirm:
		return m.viewConfirm()
	case syncRunning:
		return m.viewDashboard(false)
	case syncCanceling:
		if m.startedAt.IsZero() {
			return m.viewProbing(true)
		}
		return m.viewDashboard(true)
	case syncFinished:
		return m.viewFinished()
	}
	return ""
}

func (m syncModel) frame(title, body, help string) string {
	content := syncTitleStyle.Render(title) + "\n\n" + body
	if help != "" {
		content += "\n\n" + syncDimStyle.Render(help)
	}
	width := m.width
	if width < 60 {
		width = 60
	}
	return tui.NewStyle().Padding(1, 2).Width(width - 4).Render(content)
}

func (m syncModel) viewProbing(canceling bool) string {
	title := "Sync preflight"
	state := "Checking exact repository access before any changes"
	if canceling {
		state = "Canceling preflight"
	}
	body := fmt.Sprintf("%s\n\nCompleted %d  Started %d  Endpoints %d", state, m.probeFinished, m.probeStarted, len(m.plan.Endpoints))
	if m.currentProbe != "" {
		body += "\n\n" + syncDimStyle.Render(git.RedactRemote(m.currentProbe))
	}
	return m.frame(title, body, "ctrl+c cancel")
}

func (m syncModel) viewReview() string {
	var lines []string
	start, end := m.visibleRows()
	for index := start; index < end; index++ {
		lines = append(lines, m.renderReviewRow(index, m.rows[index]))
	}
	if len(lines) == 0 {
		lines = append(lines, syncDimStyle.Render("No repository targets found"))
	}
	body := "Choose what runs once. Failed endpoints stay disabled unless a verified SSH conversion is selected.\n\n" + strings.Join(lines, "\n")
	if m.errorText != "" {
		body += "\n\n" + syncFailedStyle.Render(m.errorText)
	}
	return m.frame("Review sync", body, "up/down navigate  space toggle  c SSH conversion  enter continue  esc cancel")
}

func (m syncModel) visibleRows() (int, int) {
	limit := m.height - 10
	if limit < 5 {
		limit = 5
	}
	if limit >= len(m.rows) {
		return 0, len(m.rows)
	}
	start := m.cursor - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > len(m.rows) {
		start = len(m.rows) - limit
	}
	return start, start + limit
}

func (m syncModel) renderReviewRow(index int, row syncReviewRow) string {
	cursor := "  "
	if index == m.cursor {
		cursor = "> "
	}
	selected, selectable := m.rowState(row)
	check := "[ ]"
	if selected {
		check = "[x]"
	}
	indent, label := reviewRowLabel(row)
	line := cursor + indent + check + " " + label + m.reviewRowConversion(row)
	if !selectable && !m.selection.ConversionAvailable(row.id) {
		line += " [unavailable]"
	}
	return styleReviewRow(line, index == m.cursor, selected, selectable || m.selection.ConversionAvailable(row.id))
}

func reviewRowLabel(row syncReviewRow) (string, string) {
	if row.kind == syncSourceRow {
		return "", "Source " + row.label
	}
	label := row.label
	if row.state != "" {
		label += " (" + string(row.state) + ")"
	}
	return "  ", label
}

func (m syncModel) reviewRowConversion(row syncReviewRow) string {
	if candidate, ok := m.selection.Conversion(row.id); ok {
		return " -> " + candidate
	}
	if m.selection.ConversionAvailable(row.id) {
		return " [c: verified SSH]"
	}
	return ""
}

func styleReviewRow(line string, current, selected, selectable bool) string {
	if !selectable {
		return syncDimStyle.Render(line)
	}
	if current {
		return syncSelectedStyle.Render(line)
	}
	if selected {
		return syncActiveStyle.Render(line)
	}
	return line
}

func (m syncModel) rowState(row syncReviewRow) (bool, bool) {
	if row.kind == syncSourceRow {
		selected := m.selection.SourceSelected(row.id)
		selectable := selected
		for _, target := range m.plan.Targets {
			if target.SourceKey == row.id && (m.selection.TargetSelectable(target.ID) || m.selection.ConversionAvailable(target.ID)) {
				selectable = true
			}
		}
		return selected, selectable
	}
	if row.kind == syncProjectRow {
		return m.selection.ProjectSelected(targetProject(m.plan, row.id)), m.selection.TargetSelectable(row.id)
	}
	return m.selection.TargetSelected(row.id), m.selection.TargetSelectable(row.id)
}

func (m syncModel) viewConfirm() string {
	projects := len(m.selection.SelectedProjects())
	targets := len(m.selection.SelectedTargets())
	conversions := len(m.selection.Conversions())
	body := fmt.Sprintf("Projects: %d\nTargets: %d\nSSH conversions: %d\n\nStart synchronization?", projects, targets, conversions)
	return m.frame("Confirm sync", body, "enter/y start  n/esc back  ctrl+c cancel")
}

func (m syncModel) viewDashboard(canceling bool) string {
	phase := "Executing"
	if canceling {
		phase = "Cancel requested; waiting for the current operation"
	}
	current := m.currentOp
	if m.currentProject != "" {
		current += " " + m.currentProject
	}
	body := fmt.Sprintf("Phase: %s\nProgress: %d/%d\nCurrent: %s\nElapsed: %s", phase, m.completed, m.total, current, formatSyncDuration(m.elapsed))
	if len(m.recent) > 0 {
		body += "\n\nRecent results:\n"
		for _, event := range m.recent {
			body += "\n" + renderRecentEvent(event)
		}
	}
	body += fmt.Sprintf("\n\nCounters: success=%d failed=%d skipped=%d", m.counters.success, m.counters.failed, m.counters.skipped)
	return m.frame("Sync dashboard", body, "ctrl+c cancel and wait")
}

func renderRecentEvent(event workspacesync.Event) string {
	label := event.Operation
	if event.Project != "" {
		label += " " + event.Project
	}
	if event.Mirror != "" {
		label += "/" + event.Mirror
	}
	line := fmt.Sprintf("%-9s %s", event.Status, label)
	if event.Status == workspacesync.ResultFailed {
		return syncFailedStyle.Render(line)
	}
	if event.Status == workspacesync.ResultSuccess {
		return syncActiveStyle.Render(line)
	}
	return syncDimStyle.Render(line)
}

func (m syncModel) viewFinished() string {
	counts := countSyncReport(m.report)
	status := "Completed"
	if m.report.Canceled {
		status = "Canceled"
	} else if classifySyncReport(m.report) != 0 {
		status = "Completed with failures"
	}
	body := fmt.Sprintf("Status: %s\nElapsed: %s\n\nSuccess: %d\nFailed: %d\nSkipped: %d\nCanceled: %d\nConflicts: %d", status, formatSyncDuration(m.elapsed), counts.success, counts.failed, counts.skipped, counts.canceled, len(m.report.Conflicts))
	return m.frame("Sync summary", body, "press any key to exit")
}

func formatSyncDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(10 * time.Millisecond).String()
	}
	return duration.Round(time.Second).String()
}
