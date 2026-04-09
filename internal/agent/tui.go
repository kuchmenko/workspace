package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/layout"
)

// View mode: browsing the project list, or floating action popup over it.
type viewMode int

const (
	viewList        viewMode = iota // nested list of groups + projects
	viewPopup                       // floating action popup over dimmed list
	viewNewWorktree                 // worktree creation form
	viewPromote                     // branch promote form
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
	Cwd       string
	ResumeID  string
	ShellOnly bool // true = exec $SHELL instead of claude
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

	// Worktree creation form state.
	wtTopic      string
	wtBranch     string // custom branch override (empty = wt/<machine>/<topic>)
	wtAutoPush   bool
	wtNoLaunch   bool   // true when "create only", false when "create + launch"
	wtField      int    // 0=topic, 1=branch, 2=auto-push, 3=confirm

	// Promote form state.
	promoteWt       Worktree
	promoteNewName  string
	promoteField    int // 0=name, 1=confirm

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
		if m.mode == viewPromote {
			return m.updatePromote(msg)
		}
		if m.mode == viewNewWorktree {
			return m.updateNewWorktree(msg)
		}
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
			case "shell":
				m.Launch = &LaunchRequest{Cwd: item.cwd, ShellOnly: true}
				return m, tea.Quit
			case "action":
				m.Launch = &LaunchRequest{Cwd: m.popupProj.Path}
				return m, tea.Quit
			case "new-worktree":
				m.mode = viewNewWorktree
				m.wtTopic = ""
				m.wtBranch = ""
				m.wtAutoPush = false
				m.wtField = 0
				return m, nil
			case "create-wt-only":
				m.mode = viewNewWorktree
				m.wtTopic = ""
				m.wtBranch = ""
				m.wtAutoPush = false
				m.wtNoLaunch = true
				m.wtField = 0
				return m, nil
			case "worktree":
				m.Launch = &LaunchRequest{Cwd: item.cwd}
				return m, tea.Quit
			case "session":
				m.Launch = &LaunchRequest{Cwd: item.cwd, ResumeID: item.resumeID}
				return m, tea.Quit
			}
		}
	case "d", "D":
		// Delete worktree (only on worktree items, not main).
		if m.popupCursor >= 0 && m.popupCursor < len(m.popupItems) {
			item := m.popupItems[m.popupCursor]
			if item.kind == "worktree" && item.cwd != m.popupProj.Path {
				err := DeleteWorktree(m.popupProj.Path, item.cwd, false)
				if err == nil {
					// Refresh popup.
					m.openPopup(m.popupProj)
				}
			}
		}
	case "s", "S":
		// Open shell in the selected worktree's directory.
		if m.popupCursor >= 0 && m.popupCursor < len(m.popupItems) {
			item := m.popupItems[m.popupCursor]
			if item.kind == "worktree" {
				m.Launch = &LaunchRequest{Cwd: item.cwd, ShellOnly: true}
				return m, tea.Quit
			}
		}
	case "p", "P":
		if m.popupCursor >= 0 && m.popupCursor < len(m.popupItems) {
			item := m.popupItems[m.popupCursor]
			if item.kind == "worktree" && item.cwd != m.popupProj.Path {
				// Find the Worktree struct for this item.
				for _, wt := range m.popupWorktrees {
					if wt.Path == item.cwd {
						m.promoteWt = wt
						// Pre-fill: if wt/machine/topic, suggest feat/<topic>.
						m.promoteNewName = suggestPromoteName(wt)
						m.promoteField = 0
						m.mode = viewPromote
						break
					}
				}
			}
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
		{label: "📂 Open shell here", kind: "shell", cwd: p.Path},
		{label: "⚡ New claude session", kind: "action", cwd: p.Path},
		{label: "🌿 New worktree + session", kind: "new-worktree"},
		{label: "🌿 Create worktree (no launch)", kind: "create-wt-only"},
	}

	// Worktrees.
	m.popupWorktrees = LoadWorktrees(p.Path)
	if len(m.popupWorktrees) > 0 {
		m.popupItems = append(m.popupItems, popupItem{kind: "separator", label: "── worktrees"})
		for _, wt := range m.popupWorktrees {
			name := worktreeDisplayName(wt)
			branchInfo := ""
			if wt.Branch != "" && !wt.IsMain {
				branchInfo = fmt.Sprintf("  (%s)", wt.Branch)
			}
			label := fmt.Sprintf("🌿 %s%s", name, branchInfo)
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

func (m *Model) updateNewWorktree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = viewPopup
		return m, nil
	case "tab", "down":
		m.wtField = (m.wtField + 1) % 4
		return m, nil
	case "shift+tab", "up":
		m.wtField = (m.wtField + 3) % 4
		return m, nil
	case "enter":
		if m.wtField == 3 { // confirm
			return m.executeNewWorktree()
		}
		// On other fields, tab forward
		m.wtField = (m.wtField + 1) % 4
		return m, nil
	case " ":
		if m.wtField == 2 { // auto-push toggle
			m.wtAutoPush = !m.wtAutoPush
			return m, nil
		}
	case "backspace":
		switch m.wtField {
		case 0:
			if len(m.wtTopic) > 0 {
				m.wtTopic = m.wtTopic[:len(m.wtTopic)-1]
			}
		case 1:
			if len(m.wtBranch) > 0 {
				m.wtBranch = m.wtBranch[:len(m.wtBranch)-1]
			}
		}
		return m, nil
	default:
		// Type into current text field.
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			switch m.wtField {
			case 0:
				m.wtTopic += key
			case 1:
				m.wtBranch += key
			}
		}
	}
	return m, nil
}

func (m *Model) executeNewWorktree() (tea.Model, tea.Cmd) {
	branch := strings.TrimSpace(m.wtBranch)
	topic := strings.TrimSpace(m.wtTopic)

	// When branch is set, topic is auto-derived from branch.
	// When neither is set, reject.
	if branch == "" && topic == "" {
		return m, nil
	}
	if branch != "" && topic == "" {
		topic = layout.SlugifyBranch(branch)
	}

	result, err := CreateWorktree(m.popupProj, topic, branch, m.wtAutoPush)
	if err != nil {
		m.mode = viewPopup
		return m, nil
	}

	// Check if we came from "create-wt-only" — don't launch, just refresh.
	for _, item := range m.popupItems {
		if item.kind == "create-wt-only" && m.popupItems[m.popupCursor].kind == "create-wt-only" {
			// This was triggered from the non-launch variant.
			// Can't easily distinguish here, so check a flag.
			break
		}
	}

	// If the popup had "create-wt-only" selected, go back to popup.
	if m.wtNoLaunch {
		m.wtNoLaunch = false
		m.openPopup(m.popupProj) // refresh to show new worktree
		return m, nil
	}

	m.Launch = &LaunchRequest{Cwd: result.Path}
	return m, tea.Quit
}

func (m *Model) updatePromote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = viewPopup
		return m, nil
	case "tab", "down":
		m.promoteField = (m.promoteField + 1) % 2
	case "shift+tab", "up":
		m.promoteField = (m.promoteField + 1) % 2
	case "enter":
		if m.promoteField == 1 {
			return m.executePromote()
		}
		m.promoteField = 1
	case "backspace":
		if m.promoteField == 0 && len(m.promoteNewName) > 0 {
			m.promoteNewName = m.promoteNewName[:len(m.promoteNewName)-1]
		}
	default:
		if m.promoteField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.promoteNewName += key
		}
	}
	return m, nil
}

func (m *Model) executePromote() (tea.Model, tea.Cmd) {
	newName := strings.TrimSpace(m.promoteNewName)
	if newName == "" {
		return m, nil
	}
	err := PromoteWorktree(m.popupProj.Path, m.promoteWt, newName)
	if err != nil {
		// TODO: show error. For now just go back.
		m.mode = viewPopup
		return m, nil
	}
	m.openPopup(m.popupProj) // refresh
	return m, nil
}

func (m *Model) viewPromote() string {
	popupW := 50
	if m.width < 56 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	oldName := m.promoteWt.Branch
	displayOld := worktreeDisplayName(m.promoteWt)

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("🔄 Promote %s", displayOld)))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("current: %s", oldName)))
	lines = append(lines, "")

	// Field 0: new branch name.
	nameLabel := "  New branch name:"
	nameVal := m.promoteNewName + "█"
	if m.promoteField != 0 {
		nameVal = m.promoteNewName
		if nameVal == "" {
			nameVal = "(required)"
		}
	}
	if m.promoteField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(nameLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+nameVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(nameLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+nameVal))
	}
	lines = append(lines, "")

	// Field 1: confirm.
	confirmLabel := "  → Rename branch"
	if m.promoteField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(confirmLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(confirmLabel))
	}
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  enter:confirm  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
}

func suggestPromoteName(wt Worktree) string {
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return "feat/" + parts[2]
		}
	}
	return wt.Branch
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
	if m.mode == viewPromote {
		return m.viewPromote()
	}
	if m.mode == viewNewWorktree {
		return m.viewNewWorktree()
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
	lines = append(lines, popupDimStyle.Width(innerW).Render("enter  s:shell  d:del  p:promote  esc:back"))

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

func (m *Model) viewNewWorktree() string {
	p := m.popupProj
	popupW := 50
	if m.width < 56 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("🌿 New worktree for %s", p.Name)))
	lines = append(lines, "")

	// When branch is provided, topic is auto-derived (slugified branch).
	// When branch is empty, topic is the primary input.
	hasBranch := strings.TrimSpace(m.wtBranch) != ""

	// Field 0: topic
	topicLabel := "  Topic:"
	if hasBranch {
		topicLabel = "  Topic (auto from branch):"
	}
	var topicDisplay string
	if hasBranch {
		topicDisplay = layout.SlugifyBranch(m.wtBranch)
	} else if m.wtField == 0 {
		topicDisplay = m.wtTopic + "█"
	} else {
		topicDisplay = m.wtTopic
		if topicDisplay == "" {
			topicDisplay = "(required if no branch)"
		}
	}
	if m.wtField == 0 && !hasBranch {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(topicLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+topicDisplay))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(topicLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+topicDisplay))
	}
	lines = append(lines, "")

	// Field 1: branch override (optional — but if set, overrides topic for path)
	branchLabel := "  Branch:"
	branchDefault := fmt.Sprintf("wt/<machine>/%s", m.wtTopic)
	if m.wtTopic == "" && !hasBranch {
		branchDefault = "wt/<machine>/<topic>"
	}
	branchVal := m.wtBranch + "█"
	if m.wtField != 1 {
		branchVal = m.wtBranch
		if branchVal == "" {
			branchVal = branchDefault
		}
	}
	if m.wtField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+branchVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+branchVal))
	}
	// Show resulting path preview.
	pathPreview := ""
	if hasBranch {
		pathPreview = fmt.Sprintf("  → dir: %s-wt-<machine>-%s", p.Name, layout.SlugifyBranch(m.wtBranch))
	} else if m.wtTopic != "" {
		pathPreview = fmt.Sprintf("  → dir: %s-wt-<machine>-%s", p.Name, m.wtTopic)
	}
	if pathPreview != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render(pathPreview))
	}
	lines = append(lines, "")

	// Field 2: auto-push toggle
	pushCheck := "☐"
	if m.wtAutoPush {
		pushCheck = "☑"
	}
	pushLabel := fmt.Sprintf("  %s Auto-push (daemon pushes this branch)", pushCheck)
	if m.wtField == 2 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(pushLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(pushLabel))
	}
	if m.wtBranch == "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render("    (wt/* branches auto-push by default)"))
	}
	lines = append(lines, "")

	// Field 3: confirm button
	confirmLabel := "  → Create & launch claude"
	if m.wtField == 3 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(confirmLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(confirmLabel))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  space:toggle  enter:confirm  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
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
