package cli

import (
	"fmt"
	"strings"
)

func (m migrateModel) View() string {
	switch m.step {
	case mStepPlan:
		return m.viewPlan()
	case mStepDecision:
		return m.viewDecision()
	case mStepMigrating:
		return m.viewMigrating()
	case mStepDone:
		return m.viewDone()
	}
	return ""
}

func (m migrateModel) viewPlan() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrate plan "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	rows := []struct {
		state migrateState
		mark  string
	}{
		{mstReady, bsArrowStyle.Render("→")},
		{mstDirty, bsWarnStyle.Render("●")},
		{mstStash, bsWarnStyle.Render("●")},
		{mstDetached, bsWarnStyle.Render("●")},
		{mstAlready, bsCheckStyle.Render("✓")},
		{mstMissing, bsDimStyle.Render("⊘")},
		{mstNotRepo, bsErrStyle.Render("✗")},
	}
	for _, row := range rows {
		items := m.plan.Bucket(row.state)
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s (%d)\n", row.mark, bsHeaderStyle.Render(row.state.label()), len(items))
		max := len(items)
		if max > 8 {
			max = 8
		}
		for i := 0; i < max; i++ {
			fmt.Fprintf(&b, "      %s\n", items[i].Name)
		}
		if len(items) > max {
			fmt.Fprintf(&b, "      %s\n", bsDimStyle.Render(fmt.Sprintf("… and %d more", len(items)-max)))
		}
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[Y] proceed   [n/esc] cancel"))
	return b.String()
}

func (m migrateModel) viewDecision() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Decision needed "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  Project: %s\n", bsHeaderStyle.Render(m.current.Name))
	fmt.Fprintf(&b, "  State:   %s\n\n", bsWarnStyle.Render(m.current.State.label()))

	switch m.current.State {
	case mstDirty:
		b.WriteString("  Working tree has uncommitted changes.\n\n")
		b.WriteString("    [w] snapshot to wt/" + m.machine + "/migration-wip-<ts> and migrate\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	case mstStash:
		b.WriteString("  Repository has stash entries (would be lost on bare clone).\n\n")
		b.WriteString("    [b] convert each stash to wt/" + m.machine + "/migration-stash-<ts>-N branch and migrate\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	case mstDetached:
		b.WriteString("  HEAD is detached. Migration needs to attach to a branch.\n\n")
		b.WriteString("    [c] checkout default_branch (orphaned commits saved to wt/" + m.machine + "/migration-detached-<ts>)\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("press the bracketed letter to choose"))
	return b.String()
}

func (m migrateModel) viewMigrating() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrating "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	total := len(m.queue)
	done := m.cursor
	bar := renderProgressBar(done, total, 30)
	fmt.Fprintf(&b, "  %s  %d / %d\n\n", bar, done, total)

	if m.cursor < total {
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), m.current.Name)
		fmt.Fprintf(&b, "    %s\n", bsDimStyle.Render(m.current.Project.Path))
	}

	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n%s %d failed (full errors after exit)\n",
			bsErrStyle.Render("✗"), len(m.errors))
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[ctrl+c] abort"))
	return b.String()
}

func (m migrateModel) viewDone() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrate finished "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d migrated\n", bsCheckStyle.Render("✓"), len(m.successes))
	if m.skipped > 0 {
		fmt.Fprintf(&b, "  %s %d skipped\n", bsDimStyle.Render("⊘"), m.skipped)
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "  %s %d failed\n", bsErrStyle.Render("✗"), len(m.errors))
		b.WriteString("\n")
		b.WriteString(bsDimStyle.Render("  Full errors will be printed after exit."))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[any key] exit"))
	return b.String()
}
