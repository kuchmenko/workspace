package aliasmgr

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
)

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = Result{
				Confirmed: true,
				Aliases:   m.buildAliasMap(),
			}
			return m, tea.Quit
		case "n", "N":
			m.result = Result{Cancelled: true}
			return m, tea.Quit
		case "esc":
			m.step = stepManage
			m.stepChangedAt = time.Now()
			return m, nil
		}
	}
	return m, nil
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" ws alias "))
	b.WriteString("  Confirm\n\n")

	aliases := m.buildAliasMap()
	if len(aliases) == 0 {
		b.WriteString("  " + dimStyle.Render("no aliases configured") + "\n\n")
		b.WriteString(helpStyle.Render("  Save empty? "))
		b.WriteString(selectedStyle.Render("y"))
		b.WriteString(helpStyle.Render("/"))
		b.WriteString(dimStyle.Render("n"))
		b.WriteString(helpStyle.Render("  (esc back)"))
		return b.String()
	}

	// Build a temporary workspace view so we can resolve via the alias package.
	tmp := &config.Workspace{
		Projects: m.ws.Projects,
		Groups:   m.ws.Groups,
		Aliases:  aliases,
	}
	resolved := alias.ResolveAll(tmp, m.root)

	// Sort by name for stable display.
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Name < resolved[j].Name })

	for _, r := range resolved {
		nameCol := selectedStyle.Render(fmt.Sprintf("%-12s", r.Name))
		path := r.Path
		warning := ""
		if r.Kind == alias.TargetUnknown {
			path = errStyle.Render("(broken target)")
		}
		if conflictPath, conflict := alias.ShellConflict(r.Name); conflict {
			warning = warnStyle.Render(fmt.Sprintf("  ⚠ shadows %s", conflictPath))
		}
		b.WriteString(fmt.Sprintf("  %s  →  %s%s\n",
			nameCol, dimStyle.Render(path), warning))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("  Save %d aliases to workspace.toml? ", len(aliases))))
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(helpStyle.Render("/"))
	b.WriteString(dimStyle.Render("n"))
	b.WriteString(helpStyle.Render("  (esc back)"))
	return b.String()
}
