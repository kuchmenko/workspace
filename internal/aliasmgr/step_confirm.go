package aliasmgr

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m Model) updateConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = Result{
				Confirmed: true,
				Aliases:   m.buildAliasMap(),
			}
			return m, tui.Quit
		case "n", "N":
			m.result = Result{Canceled: true}
			return m, tui.Quit
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

	tmp := &config.Workspace{
		Projects: m.ws.Projects,
		Groups:   m.ws.Groups,
		Aliases:  aliases,
	}
	resolved := alias.ResolveAll(tmp, m.root)

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
		fmt.Fprintf(&b, "  %s  →  %s%s\n",
			nameCol, dimStyle.Render(path), warning)
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("  Save %d aliases to workspace.toml? ", len(aliases))))
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(helpStyle.Render("/"))
	b.WriteString(dimStyle.Render("n"))
	b.WriteString(helpStyle.Render("  (esc back)"))
	return b.String()
}
