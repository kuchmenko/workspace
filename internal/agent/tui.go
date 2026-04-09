package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View mode: browsing the project list, or floating action popup over it.
type viewMode int

const (
	viewList  viewMode = iota // nested list of groups + projects
	viewPopup                 // floating action popup over dimmed list
)

// popupItem is one selectable row in the project action popup.
type popupItem struct {
	label    string
	kind     string // "action", "worktree", "session", "separator"
	cwd      string // for worktree/session launch
	resumeID string // for session resume
}

// listItem is one row in the nested list — either a group header or a project.
type listItem struct {
	kind    NodeKind
	group   string  // group name (for KindGroup rows)
	project *Project
	indent  int
}

// LaunchRequest is set when the user selects an action that should
// launch claude after the TUI exits. The CLI layer reads this from
// the model and calls LaunchClaude.
type LaunchRequest struct {
	Cwd      string
	ResumeID string
}

// Model is the bubbletea model for the agent TUI wizard.
type Model struct {
	workspaces []WorkspaceData
	mode       viewMode
	items      []listItem   // flattened visible items
	cursor     int
	expanded   map[string]bool // group name → expanded
	scroll     int           // scroll offset for long lists

	// Popup state.
	popupProj      *Project
	popupCursor    int
	popupItems     []popupItem
	popupWorktrees []Worktree
	popupSessions  []Session

	// Set when the user picks a launch action.
	Launch *LaunchRequest

	width, height int
}

// NewModel constructs the TUI model from loaded workspace data.
func NewModel(workspaces []WorkspaceData) *Model {
	m := &Model{
		workspaces: workspaces,
		mode:       viewList,
		expanded:   make(map[string]bool),
	}
	// Auto-expand all groups initially.
	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			m.expanded[g] = true
		}
	}
	m.rebuildItems()
	return m
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.mode == viewPopup {
			return m.updatePopup(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.ensureVisible()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case "enter", "l", "right":
		if m.cursor < len(m.items) {
			item := m.items[m.cursor]
			switch item.kind {
			case KindGroup:
				m.expanded[item.group] = !m.expanded[item.group]
				m.rebuildItems()
				m.ensureVisible()
			case KindProject:
				m.openPopup(item.project)
			}
		}
	case "h", "left":
		if m.cursor < len(m.items) {
			item := m.items[m.cursor]
			if item.kind == KindProject && item.project.Group != "" {
				m.expanded[item.project.Group] = false
				m.rebuildItems()
				for i, it := range m.items {
					if it.kind == KindGroup && it.group == item.project.Group {
						m.cursor = i
						break
					}
				}
				m.ensureVisible()
			} else if item.kind == KindGroup && m.expanded[item.group] {
				m.expanded[item.group] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}
	case "G":
		m.cursor = len(m.items) - 1
		m.ensureVisible()
	case "g":
		m.cursor = 0
		m.scroll = 0
	}
	return m, nil
}

func (m *Model) updatePopup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "h", "left":
		m.mode = viewList
	case "j", "down":
		for next := m.popupCursor + 1; next < len(m.popupItems); next++ {
			if m.popupItems[next].kind != "separator" {
				m.popupCursor = next
				break
			}
		}
	case "k", "up":
		for prev := m.popupCursor - 1; prev >= 0; prev-- {
			if m.popupItems[prev].kind != "separator" {
				m.popupCursor = prev
				break
			}
		}
	case "enter", "l", "right":
		if m.popupCursor >= 0 && m.popupCursor < len(m.popupItems) {
			item := m.popupItems[m.popupCursor]
			switch item.kind {
			case "action":
				m.Launch = &LaunchRequest{Cwd: m.popupProj.Path}
				return m, tea.Quit
			case "worktree":
				m.Launch = &LaunchRequest{Cwd: item.cwd}
				return m, tea.Quit
			case "session":
				m.Launch = &LaunchRequest{Cwd: item.cwd, ResumeID: item.resumeID}
				return m, tea.Quit
			}
			// separator — do nothing
		}
	}
	return m, nil
}

func (m *Model) openPopup(p *Project) {
	m.mode = viewPopup
	m.popupProj = p
	m.popupCursor = 0

	// Build popup items: actions, then worktrees, then sessions.
	m.popupItems = []popupItem{
		{label: "⚡ New claude session", kind: "action", cwd: p.Path},
	}

	// Worktrees.
	m.popupWorktrees = LoadWorktrees(p.Path)
	if len(m.popupWorktrees) > 0 {
		m.popupItems = append(m.popupItems, popupItem{kind: "separator", label: "── worktrees"})
		for _, wt := range m.popupWorktrees {
			label := fmt.Sprintf("🌿 %s", wt.Branch)
			if wt.IsMain {
				label = "🌿 main"
			}
			m.popupItems = append(m.popupItems, popupItem{
				label: label, kind: "worktree", cwd: wt.Path,
			})
		}
	}

	// Sessions.
	var searchPaths []string
	searchPaths = append(searchPaths, p.Path)
	for _, wt := range m.popupWorktrees {
		searchPaths = append(searchPaths, wt.Path)
	}
	m.popupSessions = LoadSessions(searchPaths)
	if len(m.popupSessions) > 0 {
		m.popupItems = append(m.popupItems, popupItem{kind: "separator", label: "── sessions"})
		limit := len(m.popupSessions)
		if limit > 10 {
			limit = 10
		}
		for _, s := range m.popupSessions[:limit] {
			label := fmt.Sprintf("💬 %s  %s", TimeAgo(s.Updated), s.Title)
			m.popupItems = append(m.popupItems, popupItem{
				label: label, kind: "session", cwd: s.Cwd, resumeID: s.ID,
			})
		}
	}
}

func (m *Model) ensureVisible() {
	// Keep cursor pinned to the vertical center of the viewport.
	// The list scrolls so the selected item is always at screen middle.
	maxVisible := m.listHeight()
	m.scroll = m.cursor - maxVisible/2
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > len(m.items)-maxVisible {
		m.scroll = len(m.items) - maxVisible
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *Model) listHeight() int {
	h := m.height - 4 // header + footer + borders
	if h < 3 {
		h = 3
	}
	return h
}

// rebuildItems flattens the workspace tree into a visible list,
// respecting group expansion state.
func (m *Model) rebuildItems() {
	m.items = nil
	for _, ws := range m.workspaces {
		// Ungrouped projects first.
		for i := range ws.Projects {
			p := &ws.Projects[i]
			if p.Group == "" {
				m.items = append(m.items, listItem{kind: KindProject, project: p, indent: 0})
			}
		}
		// Then groups.
		for _, g := range ws.Groups {
			m.items = append(m.items, listItem{kind: KindGroup, group: g, indent: 0})
			if m.expanded[g] {
				for i := range ws.Projects {
					p := &ws.Projects[i]
					if p.Group == g {
						m.items = append(m.items, listItem{kind: KindProject, project: p, indent: 1})
					}
				}
			}
		}
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == viewPopup {
		return m.overlayPopup()
	}
	return m.viewList()
}

func (m *Model) viewList() string {
	listW := 60
	if m.width < 66 {
		listW = m.width - 6
	}

	var rows []string

	// Header inside the centered panel.
	rows = append(rows, headerStyle.Width(listW).Render(" ws agent "))

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

		var line string
		switch item.kind {
		case KindGroup:
			arrow := "▶"
			if m.expanded[item.group] {
				arrow = "▼"
			}
			label := fmt.Sprintf(" %s %s", arrow, item.group)
			if selected {
				line = selectedGroupStyle.Width(listW).Render(label)
			} else {
				line = groupStyle.Width(listW).Render(label)
			}
		case KindProject:
			p := item.project
			indent := strings.Repeat("  ", item.indent)
			icon := "📦"
			badges := ""
			if p.WorktreeCount > 1 {
				badges += fmt.Sprintf(" %dwt", p.WorktreeCount)
			}
			if p.SessionCount > 0 {
				badges += fmt.Sprintf(" %ds", p.SessionCount)
			}
			label := fmt.Sprintf(" %s%s %s%s", indent, icon, p.Name, dimStyle.Render(badges))
			if selected {
				line = selectedStyle.Width(listW).Render(label)
			} else {
				line = itemStyle.Width(listW).Render(label)
			}
		}
		rows = append(rows, line)
	}

	// Footer.
	pos := fmt.Sprintf(" %d/%d ", m.cursor+1, len(m.items))
	hint := "j/k enter h q"
	footer := footerStyle.Width(listW).Render(pos + strings.Repeat(" ", max(0, listW-len(pos)-len(hint)-1)) + hint)
	rows = append(rows, footer)

	panel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}

// overlayPopup renders a floating bordered panel centered on screen.
// The background is a solid dim fill — no ANSI-over-ANSI issues.
func (m *Model) overlayPopup() string {
	p := m.popupProj
	if p == nil {
		return m.viewList()
	}

	popupW := 46
	if m.width < 54 {
		popupW = m.width - 8
	}
	innerW := popupW - 6 // border + padding

	var lines []string

	// Title.
	title := fmt.Sprintf("📦 %s", p.Name)
	lines = append(lines, popupTitleStyle.Width(innerW).Render(title))

	// Info.
	info := fmt.Sprintf("branch: %s · wt: %d", p.DefaultBranch, p.WorktreeCount)
	lines = append(lines, popupDimStyle.Width(innerW).Render(info))
	lines = append(lines, popupDimStyle.Width(innerW).Render(strings.Repeat("─", innerW)))

	// Items: actions, worktrees, sessions, separators.
	for i, item := range m.popupItems {
		if item.kind == "separator" {
			lines = append(lines, popupDimStyle.Width(innerW).Render(item.label))
			continue
		}
		cursor := "  "
		if i == m.popupCursor {
			cursor = "▶ "
		}
		label := cursor + strings.TrimSpace(item.label)
		if i == m.popupCursor {
			lines = append(lines, popupSelectedStyle.Width(innerW).Render(label))
		} else {
			lines = append(lines, popupItemStyle.Width(innerW).Render(label))
		}
	}

	lines = append(lines, popupDimStyle.Width(innerW).Render(strings.Repeat("─", innerW)))
	lines = append(lines, popupDimStyle.Width(innerW).Render("j/k  enter  esc"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	// Place popup centered on a dim background that fills the viewport.
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")),
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- styles ----

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("219")).
			Background(lipgloss.Color("235"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Background(lipgloss.Color("235"))

	groupStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141")).
			Bold(true)

	selectedGroupStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("219")).
				Background(lipgloss.Color("237")).
				Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("87")).
			Background(lipgloss.Color("237")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("237")).
			SetString("──────────────────────────────────────────")

	popupBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("141")).
				Padding(1, 1)

	popupTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("219"))

	popupSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("87")).
				Background(lipgloss.Color("237"))

	popupItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	popupDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	dimOverlayStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)
