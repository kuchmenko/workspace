package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) updateJobs(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	rows := max(1, m.height-5)
	maxCursor := max(0, len(m.jobs)-1)
	switch msg.String() {
	case "esc":
		m.mode = viewList
		m.sheet = m.jobsReturnSheet
		m.jobsReturnSheet = nil
	case "j", "down":
		m.jobsCursor = min(maxCursor, m.jobsCursor+1)
	case "k", "up":
		m.jobsCursor = max(0, m.jobsCursor-1)
	case "g", "home":
		m.jobsCursor = 0
	case "G", "end":
		m.jobsCursor = maxCursor
	case "ctrl+d", "ctrl+f", "pgdn":
		m.jobsCursor = min(maxCursor, m.jobsCursor+max(1, rows/2))
	case "ctrl+u", "ctrl+b", "pgup":
		m.jobsCursor = max(0, m.jobsCursor-max(1, rows/2))
	case "enter", "y":
	}
	return m, nil
}

func (m *Model) viewJobs() string {
	w := explorerPanelWidth(m.width)
	rows := []string{headerStyle.Width(w).Render(" Jobs › session history")}
	bodyRows := max(1, m.height-2)
	detailRows := 0
	if m.jobsCursor >= 0 && m.jobsCursor < len(m.jobs) {
		job := m.jobs[m.jobsCursor]
		for _, detail := range append([]string{job.Current, job.Summary, job.Error}, job.Details...) {
			if detail != "" {
				detailRows++
			}
		}
		if m.debugLogPath != "" {
			detailRows++
		}
	}
	historyRows := max(1, bodyRows-min(detailRows, bodyRows-1))
	start, end := tui.WindowAround(m.jobsCursor, len(m.jobs), historyRows)
	for i := start; i < end; i++ {
		job := m.jobs[i]
		duration := time.Since(job.QueuedAt)
		if !job.FinishedAt.IsZero() {
			duration = job.FinishedAt.Sub(job.QueuedAt)
		}
		line := fmt.Sprintf(" %s  %-22s  %-20s %d/%d  %s", job.ID, job.Label, job.State, job.Completed, job.Total, duration.Round(time.Millisecond))
		style := itemStyle
		if i == m.jobsCursor {
			style = selectedStyle
		}
		rows = append(rows, style.Width(w).Render(tui.Truncate(line, w)))
		if i == m.jobsCursor {
			for _, detail := range append([]string{job.Current, job.Summary, job.Error}, job.Details...) {
				if detail != "" && len(rows) < m.height-1 {
					rows = append(rows, dimStyle.Width(w).Render(tui.Truncate("   "+detail, w)))
				}
			}
			if m.debugLogPath != "" && len(rows) < m.height-1 {
				rows = append(rows, dimStyle.Width(w).Render(tui.Truncate("   Log: "+m.debugLogPath, w)))
			}
		}
	}
	for len(rows) < m.height-1 {
		rows = append(rows, strings.Repeat(" ", w))
	}
	rows = append(rows, footerStyle.Width(w).Render(" j/k:move  g/G:first/last  ^d/^u:half  arrows/page keys  esc:back"))
	return tui.Place(m.width, m.height, tui.Center, tui.Center, tui.JoinVertical(tui.Left, rows...))
}
