package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) openActivity(returnSheet *sheet) {
	m.jobsReturnSheet = returnSheet
	m.activityReturnFlash = nil
	m.sheet = nil
	m.mode = viewJobs
	m.jobsDetail = false
	m.jobsDetailScroll = 0
	m.activitySearch = false
	if len(m.jobs) > 0 {
		m.jobsSelectedID = m.jobs[len(m.jobs)-1].ID
	}
}

func (m *Model) activityJobs() []*explorerJob {
	jobs := make([]*explorerJob, len(m.jobs))
	for i := range m.jobs {
		jobs[i] = m.jobs[len(m.jobs)-1-i]
	}
	return jobs
}

func (m *Model) activityCursor(jobs []*explorerJob) int {
	for i, job := range jobs {
		if job.ID == m.jobsSelectedID {
			return i
		}
	}
	if len(jobs) > 0 {
		m.jobsSelectedID = jobs[0].ID
	}
	return 0
}

func (m *Model) updateJobs(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	jobs := m.filteredActivityJobs(m.activityJobs())
	if m.activitySearch && m.activityEditing {
		return m.updateActivityEditing(msg, jobs)
	}
	if m.jobsDetail {
		return m.updateActivityDetail(msg)
	}
	return m.updateActivityFeed(msg, jobs)
}

func (m *Model) updateActivityEditing(msg tui.KeyMsg, jobs []*explorerJob) (tui.Model, tui.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.closeActivitySearch()
		return m, nil
	case "enter":
		m.activityEditing = false
		m.activityQuery.Blur()
		if len(jobs) > 0 {
			m.jobsSelectedID = jobs[0].ID
		}
		return m, nil
	default:
		var cmd tui.Cmd
		m.activityQuery, cmd = m.activityQuery.Update(msg)
		return m, cmd
	}
}

func (m *Model) updateActivityDetail(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	bodyRows := max(1, m.height-2)
	maxScroll := max(0, len(m.activityDetailLines())-bodyRows)
	switch msg.String() {
	case "q", "esc":
		m.jobsDetail = false
		m.jobsDetailScroll = 0
	case "j", "down":
		m.jobsDetailScroll = min(maxScroll, m.jobsDetailScroll+1)
	case "k", "up":
		m.jobsDetailScroll = max(0, m.jobsDetailScroll-1)
	case "g", "home":
		m.jobsDetailScroll = 0
	case "G", "end":
		m.jobsDetailScroll = maxScroll
	case "ctrl+d":
		m.jobsDetailScroll = min(maxScroll, m.jobsDetailScroll+max(1, bodyRows/2))
	case "ctrl+u":
		m.jobsDetailScroll = max(0, m.jobsDetailScroll-max(1, bodyRows/2))
	case "ctrl+f", "pgdn":
		m.jobsDetailScroll = min(maxScroll, m.jobsDetailScroll+bodyRows)
	case "ctrl+b", "pgup":
		m.jobsDetailScroll = max(0, m.jobsDetailScroll-bodyRows)
	}
	return m, nil
}

func (m *Model) updateActivityFeed(msg tui.KeyMsg, jobs []*explorerJob) (tui.Model, tui.Cmd) {
	cursor := m.activityCursor(jobs)
	switch msg.String() {
	case "/":
		m.activityOriginID = m.jobsSelectedID
		m.activitySearch = true
		m.activityEditing = true
		m.activityQuery.SetValue("")
		return m, m.activityQuery.Focus()
	case "q", "esc":
		if m.activitySearch {
			m.closeActivitySearch()
			return m, nil
		}
		m.closeActivity()
	case "j", "down":
		cursor = min(max(0, len(jobs)-1), cursor+1)
	case "k", "up":
		cursor = max(0, cursor-1)
	case "g", "home":
		cursor = 0
	case "G", "end":
		cursor = max(0, len(jobs)-1)
	case "enter":
		if len(jobs) > 0 {
			m.jobsDetail = true
			m.jobsDetailScroll = 0
		}
	}
	if cursor < len(jobs) {
		m.jobsSelectedID = jobs[cursor].ID
		m.jobsCursor = cursor
	}
	return m, nil
}

func (m *Model) closeActivity() {
	returnSheet, returnFlash := m.jobsReturnSheet, m.activityReturnFlash
	m.jobsReturnSheet, m.activityReturnFlash = nil, nil
	m.mode, m.sheet = viewList, returnSheet
	if returnFlash != nil {
		m.restoreFlashRefresh(*returnFlash)
	}
}

func (m *Model) closeActivitySearch() {
	m.activityQuery.Blur()
	m.activityQuery.SetValue("")
	m.activitySearch = false
	m.activityEditing = false
	if m.findJob(m.activityOriginID) != nil {
		m.jobsSelectedID = m.activityOriginID
	}
	m.activityOriginID = ""
}

func (m *Model) filteredActivityJobs(jobs []*explorerJob) []*explorerJob {
	if !m.activitySearch {
		return jobs
	}
	query := strings.ToLower(strings.TrimSpace(m.activityQuery.Value()))
	if query == "" {
		return jobs
	}
	filtered := make([]*explorerJob, 0, len(jobs))
	for _, job := range jobs {
		parts := []string{job.ID, job.Label, job.Current, job.Summary, job.Error, activityOutcomeLabel(job.State), string(job.State)}
		parts = append(parts, job.Details...)
		for _, outcome := range job.Outcomes {
			parts = append(parts, outcome.Target, outcome.Detail, string(outcome.Kind))
		}
		if strings.Contains(strings.ToLower(strings.Join(parts, " ")), query) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func (m *Model) viewJobs() string {
	if m.jobsDetail {
		return m.viewActivityDetail()
	}
	w := max(1, m.width)
	jobs := m.filteredActivityJobs(m.activityJobs())
	cursor := m.activityCursor(jobs)
	running, attention := 0, 0
	for _, job := range m.jobs {
		if job.State == jobRunning {
			running++
		}
		if job.State == jobFailed || job.State == jobPartial {
			attention++
		}
	}
	header := padPanelRight(" Activity", fmt.Sprintf("%d running · %d attention ", running, attention), w)
	rows := []string{headerStyle.Width(w).Render(header)}
	if m.activitySearch {
		rows = append(rows, flashSearchStyle.Width(w).Render(" "+iconSearch+" "+m.activityQuery.View()))
	}
	bodyRows := max(1, m.height-len(rows)-1)
	start, end := tui.WindowAround(cursor, len(jobs), bodyRows)
	for i := start; i < end; i++ {
		job := jobs[i]
		line := formatActivityRow(job, w-1)
		if i == cursor {
			rows = append(rows, accentBarStyle.Render("▌")+selectedStyle.Width(w-1).Render(tui.Truncate(line, w-1)))
		} else {
			rows = append(rows, activityStyle(job.State).Width(w).Render(" "+tui.Truncate(line, w-1)))
		}
	}
	for len(rows) < m.height-1 {
		rows = append(rows, strings.Repeat(" ", w))
	}
	footer := " Enter details · / search · q back"
	if m.activityEditing {
		footer = " Enter results · Ctrl+C cancel"
	}
	rows = append(rows, footerStyle.Width(w).Render(tui.Truncate(footer, w)))
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

func formatActivityRow(job *explorerJob, width int) string {
	timestamp := job.QueuedAt.Format("15:04")
	if job.QueuedAt.IsZero() {
		timestamp = "--:--"
	}
	left := fmt.Sprintf("%s  %s %s", timestamp, activityStateLabel(job.State), presentLabel(job.Label))
	right := activityOutcomeLabel(job.State)
	if job.State == jobRunning || job.State == jobQueued {
		right = fmt.Sprintf("%d/%d", job.Completed, job.Total)
		if job.Current != "" {
			right += " · " + presentLabel(job.Current)
		}
	}
	return padPanelRight(left, right, width)
}

func activityStateLabel(state jobState) string {
	switch state {
	case jobRunning:
		return "▶"
	case jobQueued:
		return "…"
	case jobFailed:
		return "×"
	case jobPartial:
		return "!"
	default:
		return "✓"
	}
}

func activityOutcomeLabel(state jobState) string {
	if state == jobPartial {
		return "completed with issues"
	}
	return string(state)
}

func activityStyle(state jobState) tui.Style {
	if state == jobComplete {
		return dimStyle
	}
	if state == jobFailed || state == jobPartial {
		return statusMsgStyle
	}
	return itemStyle
}

func (m *Model) viewActivityDetail() string {
	w := max(1, m.width)
	job := m.findJob(m.jobsSelectedID)
	if job == nil {
		m.jobsDetail = false
		return m.viewJobs()
	}
	header := padPanelRight(" "+presentLabel(job.Label), activityOutcomeLabel(job.State)+" ", w)
	body := m.activityDetailLines()
	bodyRows := max(1, m.height-2)
	start := min(m.jobsDetailScroll, max(0, len(body)-bodyRows))
	end := min(len(body), start+bodyRows)
	rows := []string{headerStyle.Width(w).Render(header)}
	for _, line := range body[start:end] {
		rows = append(rows, tui.Truncate(line, w))
	}
	for len(rows) < m.height-1 {
		rows = append(rows, strings.Repeat(" ", w))
	}
	rows = append(rows, footerStyle.Width(w).Render(tui.Truncate(" j/k scroll · g/G first/last · ^d/^u half · q back", w)))
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

func (m *Model) activityDetailLines() []string {
	job := m.findJob(m.jobsSelectedID)
	if job == nil {
		return nil
	}
	rows := []string{""}
	if len(job.Outcomes) > 0 {
		for _, outcome := range job.Outcomes {
			symbol := "✓"
			if outcome.Kind != targetSuccess {
				symbol = "!"
			}
			rows = append(rows, fmt.Sprintf(" %s %s", symbol, presentLabel(outcome.Target)))
			if outcome.Detail != "" {
				rows = append(rows, dimStyle.Render("   "+presentLabel(outcome.Detail)))
			}
			rows = append(rows, "")
		}
	} else {
		for _, detail := range append([]string{job.Summary, job.Error}, job.Details...) {
			if detail != "" {
				rows = append(rows, " "+presentLabel(detail))
			}
		}
	}
	if !job.FinishedAt.IsZero() {
		rows = append(rows, dimStyle.Render(" Submitted "+job.QueuedAt.Format(time.RFC3339)))
	}
	return rows
}
