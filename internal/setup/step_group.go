package setup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	gh "github.com/kuchmenko/workspace/internal/github"
)

type groupModel struct {
	groups      []GroupEntry
	cursor      int // group index
	repoCursor  int // repo index within group (-1 = group header)
	editing     bool
	editInput   textinput.Model
	moving      bool // moving a repo
	moveFrom    int  // source group index
	moveRepoIdx int  // source repo index
	width       int
	height      int
	username    string
}

func newGroupModel(repos []gh.Repo, username string, w, h int) groupModel {
	// Auto-group by owner
	groupMap := make(map[string][]gh.Repo)
	var order []string

	for _, r := range repos {
		groupName := r.Owner
		if r.Owner == username {
			groupName = "personal"
		}
		if _, exists := groupMap[groupName]; !exists {
			order = append(order, groupName)
		}
		groupMap[groupName] = append(groupMap[groupName], r)
	}

	groups := make([]GroupEntry, 0, len(order))
	for _, name := range order {
		groups = append(groups, GroupEntry{
			Name:  name,
			Repos: groupMap[name],
		})
	}

	ti := textinput.New()
	ti.CharLimit = 40

	return groupModel{
		groups:     groups,
		repoCursor: -1,
		editInput:  ti,
		width:      w,
		height:     h,
		username:   username,
	}
}

func (m groupModel) totalItems() int {
	n := 0
	for _, g := range m.groups {
		n += 1 + len(g.Repos) // header + repos
	}
	return n
}

// flatIndex returns (groupIdx, repoIdx) where repoIdx=-1 means the group header.
func (m groupModel) flatToGroupRepo(flat int) (int, int) {
	pos := 0
	for gi, g := range m.groups {
		if pos == flat {
			return gi, -1
		}
		pos++
		for ri := range g.Repos {
			if pos == flat {
				return gi, ri
			}
			pos++
		}
	}
	return 0, -1
}

func (m groupModel) groupRepoToFlat(gi, ri int) int {
	pos := 0
	for i, g := range m.groups {
		if i == gi && ri == -1 {
			return pos
		}
		pos++
		for j := range g.Repos {
			if i == gi && j == ri {
				return pos
			}
			pos++
		}
	}
	return 0
}

func (m groupModel) flatCursor() int {
	return m.groupRepoToFlat(m.cursor, m.repoCursor)
}

func (m groupModel) update(msg tea.Msg) (groupModel, tea.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}
	if m.moving {
		return m.updateMoving(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	total := m.totalItems()
	flat := m.flatCursor()

	switch key.String() {
	case "up", "k":
		if flat > 0 {
			m.cursor, m.repoCursor = m.flatToGroupRepo(flat - 1)
		}

	case "down", "j":
		if flat < total-1 {
			m.cursor, m.repoCursor = m.flatToGroupRepo(flat + 1)
		}

	case "r":
		// Rename current group
		if m.repoCursor == -1 && m.cursor < len(m.groups) {
			m.editing = true
			m.editInput.SetValue(m.groups[m.cursor].Name)
			m.editInput.Focus()
			return m, nil
		}

	case "m":
		// Move current repo to another group
		if m.repoCursor >= 0 {
			m.moving = true
			m.moveFrom = m.cursor
			m.moveRepoIdx = m.repoCursor
		}

	case "n":
		// New empty group
		m.groups = append(m.groups, GroupEntry{Name: "new-group"})
		m.cursor = len(m.groups) - 1
		m.repoCursor = -1
		m.editing = true
		m.editInput.SetValue("new-group")
		m.editInput.Focus()
		return m, nil
	}

	return m, nil
}

func (m groupModel) updateEditing(msg tea.Msg) (groupModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			newName := strings.TrimSpace(m.editInput.Value())
			if newName != "" {
				m.groups[m.cursor].Name = newName
			}
			m.editing = false
			return m, nil
		case "escape":
			m.editing = false
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

func (m groupModel) updateMoving(msg tea.Msg) (groupModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "escape":
			m.moving = false
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.groups)-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor != m.moveFrom {
				// Move repo
				repo := m.groups[m.moveFrom].Repos[m.moveRepoIdx]
				m.groups[m.moveFrom].Repos = append(
					m.groups[m.moveFrom].Repos[:m.moveRepoIdx],
					m.groups[m.moveFrom].Repos[m.moveRepoIdx+1:]...,
				)
				m.groups[m.cursor].Repos = append(m.groups[m.cursor].Repos, repo)

				// Remove empty groups
				if len(m.groups[m.moveFrom].Repos) == 0 {
					m.groups = append(m.groups[:m.moveFrom], m.groups[m.moveFrom+1:]...)
					if m.cursor > m.moveFrom {
						m.cursor--
					}
				}
			}
			m.moving = false
			m.repoCursor = -1
		}
	}
	return m, nil
}

func (m groupModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Assign groups\n")
	b.WriteString(subtitleStyle.Render("  Auto-grouped by org. Rename, move, or create new groups."))
	b.WriteString("\n\n")

	if m.moving {
		b.WriteString(selectedStyle.Render(fmt.Sprintf(
			"  Moving: %s → select target group\n\n",
			m.groups[m.moveFrom].Repos[m.moveRepoIdx].Name)))
	}

	flat := 0
	currentFlat := m.flatCursor()

	for gi, g := range m.groups {
		isCurrent := flat == currentFlat
		prefix := "  "
		if isCurrent {
			prefix = cursorStyle.Render("> ")
		}

		header := g.Name
		if m.editing && gi == m.cursor && m.repoCursor == -1 {
			header = m.editInput.View()
		} else if isCurrent {
			header = selectedStyle.Render(header)
		} else {
			header = groupHeaderStyle.Render(header)
		}

		if m.moving && gi == m.cursor {
			header += selectedStyle.Render(" ← move here")
		}

		b.WriteString(fmt.Sprintf("%s┌ %s (%d repos)\n", prefix, header, len(g.Repos)))
		flat++

		for _, r := range g.Repos {
			isCurrent = flat == currentFlat
			rPrefix := "  │  "
			if isCurrent {
				rPrefix = cursorStyle.Render("> ") + "│  "
			}

			name := r.Name
			if isCurrent {
				name = selectedStyle.Render(name)
			}

			b.WriteString(fmt.Sprintf("%s%s\n", rPrefix, name))
			flat++
		}

		b.WriteString("  └\n")
	}

	b.WriteString("\n")
	if m.moving {
		b.WriteString(helpStyle.Render("  ↑↓ select group  enter confirm  esc cancel"))
	} else {
		b.WriteString(helpStyle.Render("  ↑↓ navigate  r rename  m move  n new group  enter finish  esc back"))
	}

	return b.String()
}
