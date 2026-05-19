package cli

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m bootstrapModel) View() string {
	switch m.step {
	case bsStepPlan:
		return m.viewPlan()
	case bsStepCloning:
		return m.viewCloning()
	case bsStepBranchPrompt:
		return m.viewBranchPrompt()
	case bsStepDone:
		return m.viewDone()
	}
	return ""
}

func (m bootstrapModel) viewPlan() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Bootstrap plan "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	rows := []struct {
		state bootstrap.State
		label string
		mark  string
	}{
		{bootstrap.StateMissing, "will clone", bsArrowStyle.Render("→")},
		{bootstrap.StatePresent, "already present", bsCheckStyle.Render("✓")},
		{bootstrap.StateNeedsMigrate, "needs migration", bsWarnStyle.Render("⚠")},
		{bootstrap.StateBlocked, "path blocked", bsErrStyle.Render("✗")},
		{bootstrap.StateSelf, "self (skipped)", bsDimStyle.Render("⊘")},
	}
	for _, row := range rows {
		items := m.plan.Bucket(row.state)
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s (%d)\n", row.mark, bsHeaderStyle.Render(row.label), len(items))

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
	if len(m.toClone) == 0 {
		b.WriteString(bsDimStyle.Render("Nothing to clone."))
		b.WriteString("\n")
	}
	b.WriteString(bsHelpStyle.Render("[Y] proceed   [n/esc] cancel"))
	return b.String()
}

func (m bootstrapModel) viewCloning() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Cloning "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	total := len(m.toClone)
	done := m.current
	bar := renderProgressBar(done, total, 30)
	fmt.Fprintf(&b, "  %s  %d / %d\n\n", bar, done, total)

	if m.current < total {
		current := m.toClone[m.current]
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), current.Name)
		fmt.Fprintf(&b, "    %s\n", bsDimStyle.Render(current.Project.Path))
	}

	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n%s %d failed (full errors after exit)\n",
			bsErrStyle.Render("✗"), len(m.errors))
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[ctrl+c] abort"))
	return b.String()
}

func (m bootstrapModel) viewBranchPrompt() string {
	return m.branchPrompt.View()
}

func (m bootstrapModel) viewDone() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Bootstrap finished "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d cloned\n", bsCheckStyle.Render("✓"), len(m.successes))
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

func renderProgressBar(done, total, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return bsBarFilledStyle.Render(strings.Repeat("█", filled)) +
		bsBarEmptyStyle.Render(strings.Repeat("░", width-filled))
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

var (
	bsTitleStyle = tui.NewStyle().
			Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	bsHeaderStyle = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	bsDimStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	bsHelpStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	bsCheckStyle = tui.NewStyle().
			Foreground(tui.Color("2"))

	bsWarnStyle = tui.NewStyle().
			Foreground(tui.Color("3"))

	bsErrStyle = tui.NewStyle().
			Foreground(tui.Color("1"))

	bsArrowStyle = tui.NewStyle().
			Foreground(tui.Color("6"))

	bsBarFilledStyle = tui.NewStyle().
				Foreground(tui.Color("6"))

	bsBarEmptyStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	errorBannerStyle = tui.NewStyle().
				Foreground(tui.Color("1")).
				Bold(true)
)
