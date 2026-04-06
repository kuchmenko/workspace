package setup

import (
	"fmt"
	"strings"
)

type confirmModel struct {
	groups   []GroupEntry
	username string
	width    int
	height   int
}

func newConfirmModel(groups []GroupEntry, username string, w, h int) confirmModel {
	return confirmModel{
		groups:   groups,
		username: username,
		width:    w,
		height:   h,
	}
}

func (m confirmModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Confirm\n\n")

	totalRepos := 0
	for _, g := range m.groups {
		totalRepos += len(g.Repos)
	}

	b.WriteString(fmt.Sprintf("  %s groups, %s projects\n\n",
		selectedStyle.Render(fmt.Sprintf("%d", len(m.groups))),
		selectedStyle.Render(fmt.Sprintf("%d", totalRepos))))

	for _, g := range m.groups {
		b.WriteString(fmt.Sprintf("  %s\n", groupHeaderStyle.Render(g.Name)))
		for _, r := range g.Repos {
			cat := "work"
			if r.Owner == m.username {
				cat = "personal"
			}
			path := g.Name + "/" + r.Name

			b.WriteString(fmt.Sprintf("    %-30s %-10s %s\n",
				r.Name, dimStyle.Render(cat), dimStyle.Render(path)))
		}
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render("  Write workspace.toml? "))
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(helpStyle.Render("/"))
	b.WriteString(dimStyle.Render("n"))
	b.WriteString(helpStyle.Render("  (esc go back)"))

	return b.String()
}
