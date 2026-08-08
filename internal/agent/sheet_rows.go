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
		kind:    rowHeader,
		label:   fmt.Sprintf("worktrees (%d)", len(wts)),
		section: "worktrees",
	})

	ordered := make([]int, len(wts))
	for i := range wts {
		ordered[i] = i
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := wts[ordered[i]], wts[ordered[j]]
		return recencyLess(a.LastActiveAt, b.LastActiveAt, worktreeDisplayName(a), worktreeDisplayName(b), true)
	})

	for _, idx := range ordered {
		wt := &wts[idx]
		label := worktreeDisplayName(*wt)
		hint := wtHint(wt)
		rows = append(rows, sheetRow{
			kind:    rowWorktree,
			label:   label,
			hint:    hint,
			wt:      wt,
			section: "worktrees",
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
		kind:    rowHeader,
		label:   fmt.Sprintf("projects (%d)", len(projects)),
		section: "projects",
	})
	for _, p := range projects {
		rows = append(rows, sheetRow{
			kind:    rowProject,
			label:   p.Name,
			hint:    humanizeAge(p.LastActiveAt),
			proj:    p,
			section: "projects",
		})
	}

	return rows
}

func wtHint(wt *Worktree) string {
	parts := make([]string, 0, 2)
	if wt.Dirty {
		parts = append(parts, "dirty")
	} else {
		parts = append(parts, "clean")
	}
	if wt.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", wt.Ahead))
	}
	return strings.Join(parts, " ")
}
