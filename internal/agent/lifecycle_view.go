package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) viewLifecycle() string {
	lm := m.lifecycle
	lines := []string{"Lifecycle / maintenance", ""}
	switch lm.phase {
	case lifecycleSelect:
		lines = append(lines, "1 / a  Archive projects", "2 / w  Archive old worktrees")
	case lifecycleThreshold:
		lines = append(lines, "Age threshold (h/d/w/month):", lm.input+"█")
	case lifecycleReview:
		lines = append(lines, lm.message)
		if lm.action == lifecycleArchiveOldWorktrees {
			lines = append(lines, fmt.Sprintf("eligible %d · recent %d · main %d · dirty %d · protected %d · unpushed %d", len(lm.plan.Eligible), lm.plan.Recent, lm.plan.Main, lm.plan.Dirty, lm.plan.Protected, lm.plan.Unpushed))
		}
		lines = append(lines, "y / enter confirm")
	case lifecycleTypedConfirm:
		lines = append(lines, lm.message, lm.input+"█")
	case lifecycleResult:
		lines = append(lines, lm.message, "", "esc close")
	}
	if lm.errorText != "" {
		lines = append(lines, "", "Error: "+lm.errorText)
	}
	return tui.Place(m.width, m.height, tui.Center, tui.Center, popupBorderStyle.Render(strings.Join(lines, "\n")), tui.WithWhitespaceBackground(tui.Color("234")))
}
