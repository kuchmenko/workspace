package agent

import (
	"fmt"
	"sort"
	"strings"
)

func buildProjectSheetRows(m *Model, p *Project) []sheetRow {
	var rows []sheetRow

	wts, err := m.wtCache.Get(p.Path)
	if err != nil {
		m.statusMsg = "inspect worktrees: " + err.Error()
	}
	for i := range wts {
		if active := p.BranchActivity[wts[i].Branch]; active.After(wts[i].LastActiveAt) {
			wts[i].LastActiveAt = active
		}
	}

	rows = append(rows, sheetRow{
		kind:     rowHeader,
		label:    fmt.Sprintf("worktrees (%d)", len(wts)),
		hint:     "status",
		activity: "activity",
		section:  "worktrees",
	})

	ordered := make([]int, len(wts))
	for i := range wts {
		ordered[i] = i
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := wts[ordered[i]], wts[ordered[j]]
		if a.IsMain != b.IsMain {
			return a.IsMain
		}
		return recencyLess(a.LastActiveAt, b.LastActiveAt, worktreeDisplayName(a), worktreeDisplayName(b), true)
	})

	for _, idx := range ordered {
		wt := &wts[idx]
		label := worktreeDisplayName(*wt)
		hint := wtHint(wt)
		if badge := m.runnerBadge(m.worktreeRunnerTarget(p, wt), wt.Path); badge != "" {
			if hint != "" {
				hint += " "
			}
			hint += badge
		}
		activity := humanizeAge(wt.LastActiveAt)
		if activity == "" {
			activity = "—"
		}
		rows = append(rows, sheetRow{
			kind:     rowWorktree,
			label:    label,
			hint:     hint,
			activity: activity,
			wt:       wt,
			section:  "worktrees",
		})
	}

	return rows
}

func buildGroupSheetRows(m *Model, workspaceRoot, group, groupPath string) []sheetRow {
	var rows []sheetRow

	var projects []*Project
	for wi := range m.workspaces {
		ws := &m.workspaces[wi]
		if ws.Root != workspaceRoot {
			continue
		}
		for pi := range ws.Projects {
			p := &ws.Projects[pi]
			if p.Group == group {
				projects = append(projects, p)
			}
		}
	}
	sort.SliceStable(projects, func(i, j int) bool {
		ai, aj := projects[i].LastActiveAt, projects[j].LastActiveAt
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return projects[i].Name < projects[j].Name
	})

	rows = append(rows, sheetRow{
		kind:     rowHeader,
		label:    fmt.Sprintf("projects (%d)", len(projects)),
		activity: "activity",
		section:  "projects",
	})
	for _, p := range projects {
		activity := humanizeAge(p.LastActiveAt)
		if activity == "" {
			activity = "—"
		}
		rows = append(rows, sheetRow{
			kind:     rowProject,
			label:    p.Name,
			hint:     m.runnerBadge(m.projectRunnerTarget(p), p.Path),
			activity: activity,
			proj:     p,
			section:  "projects",
		})
	}

	return rows
}

func wtHint(wt *Worktree) string {
	parts := make([]string, 0, 3)
	if wt.Unknown {
		parts = append(parts, "unknown")
	} else if wt.Dirty {
		parts = append(parts, "modified")
	}
	if wt.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", wt.Ahead))
	}
	if wt.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", wt.Behind))
	}
	return strings.Join(parts, " ")
}
