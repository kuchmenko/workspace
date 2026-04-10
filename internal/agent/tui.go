package agent

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/layout"
)

// View mode.
type viewMode int

const (
	viewList        viewMode = iota // nested list — all navigation lives here
	viewNewWorktree                 // worktree creation form
	viewPromote                     // branch promote form
	viewFlash                       // flash search with jump labels
	viewPromptInput                 // optional prompt input before launching claude
)

// listItem is one row in the nested list.
type listItem struct {
	kind      NodeKind
	group     string   // group name (for KindGroup rows)
	project   *Project // for KindProject rows
	worktree  *Worktree // for KindWorktree rows
	session   *Session  // for KindPortal rows (sessions)
	indent    int
	path      string   // filesystem path for shell navigation
	parentProj *Project // for worktree/session: which project they belong to
}

// LaunchRequest is set when the user selects an action that should
// launch claude after the TUI exits. The CLI layer reads this from
// the model and calls LaunchClaude.
type LaunchRequest struct {
	Cwd       string
	ResumeID  string
	ShellOnly bool   // true = exec $SHELL instead of claude
	Prompt    string // optional initial prompt for claude (-p flag)
}

// Model is the bubbletea model for the agent TUI wizard.
type Model struct {
	workspaces []WorkspaceData
	mode       viewMode
	items      []listItem   // flattened visible items
	cursor     int
	expanded   map[string]bool // group/project name → expanded
	scroll     int           // scroll offset for long lists

	// Active project for worktree/promote forms.
	popupProj    *Project

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

	// Prompt input state (optional prompt before launch).
	pendingLaunch *LaunchRequest // set before entering prompt input
	promptInput   string

	// Flash search state.
	flashQuery   string
	flashMatches []int    // indices into m.items that match
	flashLabels  []rune   // one label per match (a, b, c, ...)

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
		// ctrl+c and ctrl+q always quit from anywhere.
		if msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			return m, tea.Quit
		}
		// ctrl+s = open shell in selected item's directory from anywhere.
		if msg.String() == "ctrl+s" {
			item := m.currentItem()
			if item != nil && item.path != "" {
				m.Launch = &LaunchRequest{Cwd: item.path, ShellOnly: true}
				return m, tea.Quit
			}
		}
		if m.mode == viewPromptInput {
			return m.updatePromptInput(msg)
		}
		if m.mode == viewFlash {
			return m.updateFlash(msg)
		}
		if m.mode == viewPromote {
			return m.updatePromote(msg)
		}
		if m.mode == viewNewWorktree {
			return m.updateNewWorktree(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	item := m.currentItem()

	switch msg.String() {
	case "q":
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

	case "enter":
		if item == nil {
			break
		}
		switch item.kind {
		case KindGroup:
			// Launch claude in group directory.
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		case KindProject:
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		case KindWorktree:
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		case KindPortal:
			if item.session != nil {
				m.Launch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
				return m, tea.Quit
			}
		}

	case "p":
		// Claude with prompt — available on group, project, worktree.
		if item != nil && item.path != "" && (item.kind == KindGroup || item.kind == KindProject || item.kind == KindWorktree) {
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		}

	case "w":
		// New worktree — only on projects.
		if item != nil && item.kind == KindProject {
			m.wtNoLaunch = true
			m.wtTopic = ""
			m.wtBranch = ""
			m.wtAutoPush = false
			m.wtField = 0
			m.popupProj = item.project
			m.mode = viewNewWorktree
			return m, nil
		}

	case "l", "right":
		if item != nil && item.path != "" {
			m.Launch = &LaunchRequest{Cwd: item.path, ShellOnly: true}
			return m, tea.Quit
		}

	case "h", "left":
		if item != nil {
			switch {
			case item.kind == KindProject && m.expanded["proj:"+item.project.ID]:
				m.expanded["proj:"+item.project.ID] = false
				m.rebuildItems()
				m.ensureVisible()
			case item.kind == KindProject && item.project.Group != "":
				m.expanded[item.project.Group] = false
				m.rebuildItems()
				m.jumpToGroup(item.project.Group)
			case (item.kind == KindWorktree || item.kind == KindPortal) && item.parentProj != nil:
				m.expanded["proj:"+item.parentProj.ID] = false
				m.rebuildItems()
				m.jumpToProject(item.parentProj.ID)
			case item.kind == KindGroup && m.expanded[item.group]:
				m.expanded[item.group] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "tab":
		// Expand/collapse — groups and projects.
		if item != nil {
			switch item.kind {
			case KindGroup:
				m.toggleExpand(item.group)
			case KindProject:
				key := "proj:" + item.project.ID
				m.expanded[key] = !m.expanded[key]
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "d":
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			DeleteWorktree(item.parentProj.Path, item.worktree.Path, false)
			m.rebuildItems()
			m.ensureVisible()
		}

	case "s", "/":
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()
	case "G":
		m.cursor = len(m.items) - 1
		m.ensureVisible()
	case "g":
		m.cursor = 0
		m.scroll = 0
	}
	return m, nil
}

func (m *Model) currentItem() *listItem {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		return &m.items[m.cursor]
	}
	return nil
}

func (m *Model) toggleExpand(key string) {
	m.expanded[key] = !m.expanded[key]
	m.rebuildItems()
	m.ensureVisible()
}

func (m *Model) jumpToGroup(group string) {
	for i, it := range m.items {
		if it.kind == KindGroup && it.group == group {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

func (m *Model) jumpToProject(projID string) {
	for i, it := range m.items {
		if it.kind == KindProject && it.project != nil && it.project.ID == projID {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

// launchNewSession creates a worktree and launches claude in it.
// For the default action (Enter on project), this creates a wt with
// a generated topic name and launches claude immediately.
func (m *Model) launchNewSession(p *Project, prompt string) (tea.Model, tea.Cmd) {
	// For now, launch in the project's main path.
	// TODO: auto-create worktree with generated topic name.
	m.pendingLaunch = &LaunchRequest{Cwd: p.Path, Prompt: prompt}
	m.promptInput = ""
	m.mode = viewPromptInput
	return m, nil
}


func (m *Model) updateNewWorktree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = viewList
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
		m.mode = viewList
		return m, nil
	}

	// If "create worktree only" (w key), go back to list.
	if m.wtNoLaunch {
		m.wtNoLaunch = false
		m.mode = viewList
		m.rebuildItems()
		m.ensureVisible()
		return m, nil
	}

	// Go to prompt input before launching.
	m.pendingLaunch = &LaunchRequest{Cwd: result.Path}
	m.promptInput = ""
	m.mode = viewPromptInput
	return m, nil
}

func (m *Model) updatePromote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
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
		m.mode = viewList
		return m, nil
	}
	m.rebuildItems() // refresh list to show renamed branch
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

func (m *Model) updatePromptInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
		m.pendingLaunch = nil
	case "enter":
		// Launch with or without prompt.
		m.pendingLaunch.Prompt = strings.TrimSpace(m.promptInput)
		m.Launch = m.pendingLaunch
		m.pendingLaunch = nil
		return m, tea.Quit
	case "backspace":
		if len(m.promptInput) > 0 {
			m.promptInput = m.promptInput[:len(m.promptInput)-1]
		}
	default:
		if len(key) == 1 && key[0] >= 32 {
			m.promptInput += key
		} else if key == "space" || key == " " {
			m.promptInput += " "
		}
	}
	return m, nil
}

func (m *Model) viewPromptInput() string {
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render("⚡ Launch claude"))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("in: %s", m.pendingLaunch.Cwd)))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  Initial prompt (optional):"))

	input := m.promptInput + "█"
	lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+input))
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Enter: launch (empty = interactive)"))
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Esc: back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("234")))
}

// jumpLabels is the alphabet used for flash jump labels.
const jumpLabels = "asdfghjklqwertyuiopzxcvbnm"

func (m *Model) updateFlash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = viewList
	case "backspace":
		if len(m.flashQuery) > 0 {
			m.flashQuery = m.flashQuery[:len(m.flashQuery)-1]
			m.recomputeFlash()
		} else {
			m.mode = viewList
		}
	case "enter":
		// Jump to first match.
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[0]
			m.ensureVisible()
		}
		m.mode = viewList
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			ch := rune(key[0])
			// Check if this character is a jump label.
			if m.flashQuery != "" {
				for i, label := range m.flashLabels {
					if ch == label && i < len(m.flashMatches) {
						m.cursor = m.flashMatches[i]
						m.ensureVisible()
						m.mode = viewList
						return m, nil
					}
				}
			}
			// Otherwise, append to query.
			m.flashQuery += key
			m.recomputeFlash()
		}
	}
	return m, nil
}

func (m *Model) recomputeFlash() {
	query := strings.ToLower(m.flashQuery)
	m.flashMatches = nil
	m.flashLabels = nil
	for i, item := range m.items {
		name := ""
		switch item.kind {
		case KindGroup:
			name = item.group
		case KindProject:
			name = item.project.Name
		}
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			labelIdx := len(m.flashMatches)
			m.flashMatches = append(m.flashMatches, i)
			if labelIdx < len(jumpLabels) {
				m.flashLabels = append(m.flashLabels, rune(jumpLabels[labelIdx]))
			} else {
				m.flashLabels = append(m.flashLabels, ' ')
			}
		}
	}
}

// flashLabelFor returns the jump label for a list item index, or 0 if
// the item is not in the current flash match set.
func (m *Model) flashLabelFor(itemIdx int) rune {
	for i, idx := range m.flashMatches {
		if idx == itemIdx {
			return m.flashLabels[i]
		}
	}
	return 0
}

// flashInlineLabel renders a name with the match highlighted and the
// jump label overlaying the character immediately after the match end,
// matching flash.nvim's visual style.
//
// Example: name="limitless", query="lim", label='a'
// → "[lim]a tless"  where [lim] is highlighted, a overlays 'i'
func flashInlineLabel(name, query string, label rune) string {
	lower := strings.ToLower(name)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 || q == "" {
		return name
	}

	matchEnd := idx + len(q)
	runes := []rune(name)

	var b strings.Builder
	// Before match.
	if idx > 0 {
		b.WriteString(string(runes[:idx]))
	}
	// Match portion — highlighted.
	b.WriteString(flashMatchStyle.Render(string(runes[idx:matchEnd])))
	// Label overlays the next character.
	b.WriteString(flashLabelStyle.Render(string(label)))
	// Rest of name (skip the overlaid character).
	if matchEnd+1 < len(runes) {
		b.WriteString(string(runes[matchEnd+1:]))
	}
	return b.String()
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
				m.addProjectItem(p, 0)
			}
		}
		// Then groups.
		for _, g := range ws.Groups {
			m.items = append(m.items, listItem{kind: KindGroup, group: g, indent: 0, path: GroupPath(ws.Root, g)})
			if m.expanded[g] {
				for i := range ws.Projects {
					p := &ws.Projects[i]
					if p.Group == g {
						m.addProjectItem(p, 1)
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

func (m *Model) addProjectItem(p *Project, indent int) {
	m.items = append(m.items, listItem{kind: KindProject, project: p, indent: indent, path: p.Path})

	// If project is expanded (tab), show worktrees + sessions inline.
	if !m.expanded["proj:"+p.ID] {
		return
	}

	wts := LoadWorktrees(p.Path)
	for i := range wts {
		wt := &wts[i]
		name := worktreeDisplayName(*wt)
		m.items = append(m.items, listItem{
			kind:       KindWorktree,
			worktree:   wt,
			indent:     indent + 1,
			path:       wt.Path,
			parentProj: p,
			group:      name,
		})
	}

	sessions := LoadSessions([]string{p.Path})
	if len(sessions) > 5 {
		sessions = sessions[:5]
	}
	for i := range sessions {
		s := &sessions[i]
		m.items = append(m.items, listItem{
			kind:       KindPortal,
			session:    s,
			indent:     indent + 1,
			path:       s.Cwd,
			parentProj: p,
		})
	}
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == viewPromptInput {
		return m.viewPromptInput()
	}
	if m.mode == viewPromote {
		return m.viewPromote()
	}
	if m.mode == viewNewWorktree {
		return m.viewNewWorktree()
	}
	return m.viewList()
}

func (m *Model) viewList() string {
	listW := 60
	if m.width < 66 {
		listW = m.width - 6
	}

	var rows []string

	// Header — show search query in flash mode.
	inFlash := m.mode == viewFlash
	if inFlash {
		searchLine := fmt.Sprintf(" 🔍 %s█", m.flashQuery)
		rows = append(rows, flashSearchStyle.Width(listW).Render(searchLine))
	} else {
		rows = append(rows, headerStyle.Width(listW).Render(" ws agent "))
	}

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

		// In flash mode: check if this item matches.
		flashLabel := rune(0)
		isMatch := true
		if inFlash {
			flashLabel = m.flashLabelFor(i)
			isMatch = flashLabel != 0
		}

		var line string
		switch item.kind {
		case KindGroup:
			arrow := "▶"
			if m.expanded[item.group] {
				arrow = "▼"
			}
			name := item.group
			if inFlash && isMatch {
				name = flashInlineLabel(name, m.flashQuery, flashLabel)
			}
			label := fmt.Sprintf(" %s %s", arrow, name)
			if inFlash && !isMatch {
				line = dimStyle.Width(listW).Render(label)
			} else if selected && !inFlash {
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
			name := p.Name
			if inFlash && isMatch {
				name = flashInlineLabel(name, m.flashQuery, flashLabel)
			}
			// Show expand indicator if project has worktrees.
			expandMark := ""
			if p.WorktreeCount > 1 {
				if m.expanded["proj:"+p.ID] {
					expandMark = "▼ "
				} else {
					expandMark = "▶ "
				}
			}
			label := fmt.Sprintf(" %s%s%s %s%s", indent, expandMark, icon, name, dimStyle.Render(badges))
			if inFlash && !isMatch {
				line = dimStyle.Width(listW).Render(label)
			} else if selected && !inFlash {
				line = selectedStyle.Width(listW).Render(label)
			} else {
				line = itemStyle.Width(listW).Render(label)
			}
		case KindWorktree:
			indent := strings.Repeat("  ", item.indent)
			name := item.group // worktreeDisplayName stored in group field
			if name == "" {
				name = "worktree"
			}
			label := fmt.Sprintf(" %s🌿 %s", indent, name)
			if selected {
				line = selectedStyle.Width(listW).Render(label)
			} else {
				line = dimStyle.Width(listW).Render(label)
			}
		case KindPortal:
			indent := strings.Repeat("  ", item.indent)
			title := "(session)"
			if item.session != nil {
				title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated), item.session.Title)
			}
			label := fmt.Sprintf(" %s💬 %s", indent, title)
			if selected {
				line = selectedStyle.Width(listW).Render(label)
			} else {
				line = dimStyle.Width(listW).Render(label)
			}
		}
		rows = append(rows, line)
	}

	// Footer.
	if inFlash {
		matchInfo := fmt.Sprintf(" %d matches ", len(m.flashMatches))
		hint := "type to filter · letter to jump · esc cancel"
		footer := footerStyle.Width(listW).Render(matchInfo + strings.Repeat(" ", max(0, listW-len(matchInfo)-len(hint)-1)) + hint)
		rows = append(rows, footer)
	} else {
		pos := fmt.Sprintf(" %d/%d ", m.cursor+1, len(m.items))
		hint := m.toolbarHint()
		footer := footerStyle.Width(listW).Render(pos + strings.Repeat(" ", max(0, listW-len(pos)-len(hint)-1)) + hint)
		rows = append(rows, footer)
	}

	panel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}

// overlayPopup renders a floating bordered panel centered on screen.
// The background is a solid dim fill — no ANSI-over-ANSI issues.

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

// toolbarHint builds a context-sensitive footer showing only the actions
// available for the currently selected item.
func (m *Model) toolbarHint() string {
	item := m.currentItem()
	if item == nil {
		return "s:find  q:quit"
	}

	var parts []string
	switch item.kind {
	case KindGroup:
		parts = append(parts, "⏎:claude", "p:+prompt", "tab:expand", "l:shell")
	case KindProject:
		parts = append(parts, "⏎:claude", "p:+prompt", "w:worktree", "tab:expand", "l:shell")
	case KindWorktree:
		parts = append(parts, "⏎:claude", "p:+prompt", "l:shell")
		if item.worktree != nil && !item.worktree.IsMain {
			parts = append(parts, "d:del")
		}
	case KindPortal:
		parts = append(parts, "⏎:resume")
	}
	parts = append(parts, "s:find")
	return strings.Join(parts, "  ")
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

	flashSearchStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("220")).
				Background(lipgloss.Color("235"))

	flashLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("197")) // pink/red — high contrast like flash.nvim

	flashMatchStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("220")) // yellow underlined match

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
