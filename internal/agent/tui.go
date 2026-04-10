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
	viewWhichKey                    // which-key action panel (? or space)
)

// Nerd Font icons.
const (
	iconProject  = "\uf487"  //  nf-oct-package
	iconWorktree = "\ue725"  //  nf-dev-git_branch
	iconSession  = "\uf4a6"  //  nf-md-message_text_outline
	iconSearch   = "\uf002"  //  nf-fa-search
	iconPromote  = "\uf021"  //  nf-fa-refresh
)

// listItem is one row in the nested list.
type listItem struct {
	kind       NodeKind
	group      string    // group name (for KindGroup rows)
	project    *Project  // for KindProject rows
	worktree   *Worktree // for KindWorktree rows
	session    *Session  // for KindPortal rows (sessions)
	indent     int
	path       string   // filesystem path for shell navigation
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
	scroll     int             // scroll offset for long lists

	// Active project for worktree/promote forms.
	popupProj *Project

	// Worktree creation form state.
	wtTopic    string
	wtBranch   string // custom branch override (empty = wt/<machine>/<topic>)
	wtAutoPush bool
	wtNoLaunch bool // true when "create only", false when "create + launch"
	wtField    int  // 0=topic, 1=branch, 2=auto-push, 3=confirm

	// Promote form state.
	promoteWt      Worktree
	promoteNewName string
	promoteField   int // 0=name, 1=confirm

	// Prompt input state (optional prompt before launch).
	pendingLaunch *LaunchRequest // set before entering prompt input
	promptInput   string

	// Flash search state.
	flashQuery   string
	flashMatches []int  // indices into m.items that match
	flashLabels  []rune // one label per match (a, b, c, ...)
	flashGlobal  bool   // S = global search (all items, even collapsed)
	savedExpanded map[string]bool // expansion state before global flash

	// Which-key state.
	whichKeyLevel int // 0 = root actions, 1 = worktree sub-menu

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
		if m.mode == viewWhichKey {
			return m.updateWhichKey(msg)
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
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
		case KindProject:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
		case KindWorktree:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
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
		// Prompt resume for sessions.
		if item != nil && item.kind == KindPortal && item.session != nil {
			m.pendingLaunch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
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

	case "m":
		// Promote worktree — only on non-main worktrees.
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			m.promoteWt = *item.worktree
			m.promoteNewName = suggestPromoteName(*item.worktree)
			m.promoteField = 0
			m.popupProj = item.parentProj
			m.mode = viewPromote
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
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "S":
		// Global search — expand everything, search all items.
		m.flashGlobal = true
		m.savedExpanded = make(map[string]bool)
		for k, v := range m.expanded {
			m.savedExpanded[k] = v
		}
		// Expand all groups and projects.
		for _, ws := range m.workspaces {
			for _, g := range ws.Groups {
				m.expanded[g] = true
			}
			for i := range ws.Projects {
				m.expanded["proj:"+ws.Projects[i].ID] = true
			}
		}
		m.rebuildItems()
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "?", " ":
		m.whichKeyLevel = 0
		m.mode = viewWhichKey

	case "G":
		m.cursor = len(m.items) - 1
		m.ensureVisible()
	case "g":
		m.cursor = 0
		m.scroll = 0
	}
	return m, nil
}

// --- which-key action panel ---

type whichKeyAction struct {
	key  string
	desc string
}

func (m *Model) whichKeyActions() []whichKeyAction {
	item := m.currentItem()
	if item == nil {
		return nil
	}

	if m.whichKeyLevel == 1 {
		// Worktree sub-menu.
		return []whichKeyAction{
			{"n", "new worktree"},
			{"", ""},
			{"esc", "back"},
		}
	}

	switch item.kind {
	case KindGroup:
		return []whichKeyAction{
			{"\u23ce", "open claude"},
			{"p", "+prompt"},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"\u23ce", "open claude"},
			{"p", "+prompt"},
			{"w", "worktree \u203a"},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindWorktree:
		actions := []whichKeyAction{
			{"\u23ce", "open claude"},
			{"p", "+prompt"},
			{"l", "shell"},
		}
		if item.worktree != nil && !item.worktree.IsMain {
			actions = append(actions, whichKeyAction{"m", "promote"})
			actions = append(actions, whichKeyAction{"d", "delete"})
		}
		actions = append(actions, whichKeyAction{"", ""})
		actions = append(actions, whichKeyAction{"esc", "close"})
		return actions
	case KindPortal:
		return []whichKeyAction{
			{"\u23ce", "resume"},
			{"p", "resume +prompt"},
			{"", ""},
			{"esc", "close"},
		}
	}
	return nil
}

func (m *Model) updateWhichKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	item := m.currentItem()

	// Handle worktree sub-level.
	if m.whichKeyLevel == 1 {
		switch key {
		case "esc":
			m.whichKeyLevel = 0
			return m, nil
		case "n":
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
		}
		return m, nil
	}

	// Root level — dispatch action.
	switch key {
	case "esc":
		m.mode = viewList
		return m, nil
	case "enter":
		m.mode = viewList
		return m.updateList(msg)
	case "p":
		m.mode = viewList
		return m.updateList(msg)
	case "w":
		if item != nil && item.kind == KindProject {
			m.whichKeyLevel = 1
			return m, nil
		}
		m.mode = viewList
	case "l":
		m.mode = viewList
		return m.updateList(msg)
	case "d":
		m.mode = viewList
		return m.updateList(msg)
	case "m":
		m.mode = viewList
		return m.updateList(msg)
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) whichKeyTitle() string {
	item := m.currentItem()
	if item == nil {
		return "actions"
	}
	if m.whichKeyLevel == 1 {
		return "worktree"
	}
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group // display name
	case KindPortal:
		if item.session != nil {
			t := item.session.Title
			if len(t) > 16 {
				t = t[:16] + "\u2026"
			}
			return t
		}
	}
	return "actions"
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
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("%s Promote %s", iconPromote, displayOld)))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("current: %s", oldName)))
	lines = append(lines, "")

	// Field 0: new branch name.
	nameLabel := "  New branch name:"
	nameVal := m.promoteNewName + "\u2588"
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
	confirmLabel := "  \u2192 Rename branch"
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
	if m.pendingLaunch == nil {
		m.mode = viewList
		return m.viewList()
	}
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render("Launch claude"))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("in: %s", m.pendingLaunch.Cwd)))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  Initial prompt (optional):"))

	input := m.promptInput + "\u2588"
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
		m.exitFlash(false)
	case "backspace":
		if len(m.flashQuery) > 0 {
			m.flashQuery = m.flashQuery[:len(m.flashQuery)-1]
			m.recomputeFlash()
		} else {
			m.exitFlash(false)
		}
	case "enter":
		// Jump to first match.
		if len(m.flashMatches) > 0 {
			m.cursor = m.flashMatches[0]
			m.ensureVisible()
		}
		m.exitFlash(true)
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			ch := rune(key[0])
			// Check if this character is a non-conflicting jump label.
			// Labels are only assigned from characters that would NOT
			// match if appended to the query, so this is unambiguous.
			if m.flashQuery != "" {
				for i, label := range m.flashLabels {
					if label != 0 && ch == label && i < len(m.flashMatches) {
						m.cursor = m.flashMatches[i]
						m.ensureVisible()
						m.exitFlash(true)
						return m, nil
					}
				}
			}
			// Not a label — append to query to narrow results.
			m.flashQuery += key
			m.recomputeFlash()
		}
	}
	return m, nil
}

// exitFlash leaves flash mode. For global search (S), if the user
// cancelled (jumped=false), restore the original expansion state.
// If they jumped to an item, keep expansions so the target is visible.
func (m *Model) exitFlash(jumped bool) {
	m.mode = viewList
	if m.flashGlobal && !jumped && m.savedExpanded != nil {
		m.expanded = m.savedExpanded
		m.savedExpanded = nil
		m.rebuildItems()
		m.ensureVisible()
	}
	m.flashGlobal = false
}

func (m *Model) recomputeFlash() {
	query := strings.ToLower(m.flashQuery)
	m.flashMatches = nil
	m.flashLabels = nil

	// Collect matches.
	for i, item := range m.items {
		name := m.itemSearchName(item)
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			m.flashMatches = append(m.flashMatches, i)
		}
	}

	// Compute non-conflicting labels: only use characters that, when
	// appended to the current query, would NOT match any item. This
	// makes label presses unambiguous — they can never be mistaken for
	// "continue typing to narrow results".
	available := m.availableJumpLabels()
	for i := 0; i < len(m.flashMatches); i++ {
		if i < len(available) {
			m.flashLabels = append(m.flashLabels, available[i])
		} else {
			m.flashLabels = append(m.flashLabels, 0) // no label — need more query chars
		}
	}
}

// availableJumpLabels returns characters safe to use as jump labels:
// letters that, if appended to the current query, would produce zero
// matches. This guarantees pressing a label always means "jump", never
// "keep filtering".
func (m *Model) availableJumpLabels() []rune {
	query := strings.ToLower(m.flashQuery)
	if query == "" {
		return nil // no labels until user types at least one char
	}
	var available []rune
	for _, r := range jumpLabels {
		extended := query + string(r)
		productive := false
		for _, item := range m.items {
			name := strings.ToLower(m.itemSearchName(item))
			if strings.Contains(name, extended) {
				productive = true
				break
			}
		}
		if !productive {
			available = append(available, r)
		}
	}
	return available
}

// itemSearchName returns the searchable text for a list item.
func (m *Model) itemSearchName(item listItem) string {
	switch item.kind {
	case KindGroup:
		return item.group
	case KindProject:
		return item.project.Name
	case KindWorktree:
		return item.group // display name
	case KindPortal:
		if item.session != nil {
			return item.session.Title
		}
	}
	return ""
}

// flashInlineLabel highlights the query match in a name and, when a
// non-zero label is available, overlays it on the character after the
// match. When label is 0 (no label assigned yet), only the match is
// highlighted — the user needs to type more chars.
func flashInlineLabel(name, query string, label rune) string {
	if query == "" {
		return name
	}
	lower := strings.ToLower(name)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 {
		return name
	}
	matchEnd := idx + len(q)
	runes := []rune(name)

	var b strings.Builder
	if idx > 0 {
		b.WriteString(string(runes[:idx]))
	}
	b.WriteString(flashMatchStyle.Render(string(runes[idx:matchEnd])))
	if label != 0 {
		// Overlay label on the next character.
		b.WriteString(flashLabelStyle.Render(string(label)))
		if matchEnd+1 < len(runes) {
			b.WriteString(string(runes[matchEnd+1:]))
		}
	} else {
		// No label — just show the rest of the name.
		if matchEnd < len(runes) {
			b.WriteString(string(runes[matchEnd:]))
		}
	}
	return b.String()
}

func (m *Model) ensureVisible() {
	// Keep cursor pinned to the vertical center of the viewport.
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

// breadcrumb derives contextual header from the current cursor position.
func (m *Model) breadcrumb() string {
	item := m.currentItem()
	if item == nil {
		return "ws"
	}
	switch item.kind {
	case KindGroup:
		return item.group + " \u203a"
	case KindProject:
		if item.project.Group != "" {
			return item.project.Group + " \u203a"
		}
		return "ws"
	case KindWorktree, KindPortal:
		if item.parentProj != nil {
			if item.parentProj.Group != "" {
				return item.parentProj.Group + " \u203a " + item.parentProj.Name
			}
			return item.parentProj.Name
		}
		return "ws"
	}
	return "ws"
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
		return "loading\u2026"
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
	if m.mode == viewWhichKey {
		return m.viewWhichKey()
	}
	return m.viewList()
}

// --- list rendering ---

func (m *Model) renderListRows(listW int, dimAll bool) []string {
	var rows []string
	inFlash := m.mode == viewFlash

	maxH := m.listHeight()
	end := m.scroll + maxH
	if end > len(m.items) {
		end = len(m.items)
	}

	// Track group boundaries for visual spacing.
	prevGroup := ""
	for i := m.scroll; i < end; i++ {
		item := m.items[i]
		selected := i == m.cursor

		// Inject empty line between groups.
		curGroup := m.itemGroupKey(item)
		if prevGroup != "" && curGroup != prevGroup {
			rows = append(rows, strings.Repeat(" ", listW))
		}
		prevGroup = curGroup

		// In flash mode: check if this item is in the match set.
		isMatch := false
		flashLabel := rune(0)
		if inFlash {
			for mi, idx := range m.flashMatches {
				if idx == i {
					isMatch = true
					if mi < len(m.flashLabels) {
						flashLabel = m.flashLabels[mi]
					}
					break
				}
			}
		}

		var line string
		switch item.kind {
		case KindGroup:
			line = m.renderGroup(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindProject:
			line = m.renderProject(item, selected, inFlash, isMatch, flashLabel, listW, dimAll)
		case KindWorktree:
			line = m.renderWorktree(item, selected, listW, dimAll, inFlash, isMatch, flashLabel)
		case KindPortal:
			line = m.renderSession(item, selected, listW, dimAll, inFlash, isMatch, flashLabel)
		}

		rows = append(rows, line)
	}
	return rows
}

// itemGroupKey returns a key that identifies the visual group boundary
// for inserting blank lines between groups.
func (m *Model) itemGroupKey(item listItem) string {
	switch item.kind {
	case KindGroup:
		return "g:" + item.group
	case KindProject:
		if item.project.Group != "" {
			return "g:" + item.project.Group
		}
		return "ungrouped"
	case KindWorktree, KindPortal:
		if item.parentProj != nil && item.parentProj.Group != "" {
			return "g:" + item.parentProj.Group
		}
		return "ungrouped"
	}
	return ""
}

func (m *Model) renderGroup(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	arrow := "\u25b8"
	if m.expanded[item.group] {
		arrow = "\u25be"
	}
	name := item.group
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}
	label := fmt.Sprintf("   %s %s", arrow, name)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, groupStyle, w)
	}
	return groupStyle.Width(w).Render(label)
}

func (m *Model) renderProject(item listItem, selected, inFlash, isMatch bool, flashLabel rune, w int, dimAll bool) string {
	p := item.project
	indent := strings.Repeat("  ", item.indent)

	expandMark := ""
	if p.WorktreeCount > 1 || p.SessionCount > 0 {
		if m.expanded["proj:"+p.ID] {
			expandMark = "\u25be "
		} else {
			expandMark = "\u25b8 "
		}
	}

	name := p.Name
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}

	// Build left part: indent + expand + icon + name
	left := fmt.Sprintf(" %s%s%s %s", indent, expandMark, iconProject, name)

	// Build right part: badges (right-aligned)
	var badgeParts []string
	if p.WorktreeCount > 1 {
		badgeParts = append(badgeParts, fmt.Sprintf("%dwt", p.WorktreeCount))
	}
	if p.SessionCount > 0 {
		badgeParts = append(badgeParts, fmt.Sprintf("%ds", p.SessionCount))
	}
	badges := strings.Join(badgeParts, " \u00b7 ")

	// Pad between left and right to fill width.
	line := m.padRight(left, badges, w)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(line)
	}
	if selected {
		return m.renderSelected(line, itemStyle, w)
	}
	// Render with styled badges.
	if badges != "" {
		leftPart := fmt.Sprintf(" %s%s%s %s", indent, expandMark, iconProject, name)
		padding := w - lipgloss.Width(leftPart) - lipgloss.Width(badges) - 1
		if padding < 1 {
			padding = 1
		}
		return itemStyle.Render(leftPart) + strings.Repeat(" ", padding) + badgeStyle.Render(badges)
	}
	return itemStyle.Width(w).Render(line)
}

func (m *Model) renderWorktree(item listItem, selected bool, w int, dimAll bool, inFlash bool, isMatch bool, flashLabel rune) string {
	indent := strings.Repeat("  ", item.indent)
	name := item.group // worktreeDisplayName stored in group field
	if name == "" {
		name = "worktree"
	}
	if inFlash && isMatch {
		name = flashInlineLabel(name, m.flashQuery, flashLabel)
	}
	label := fmt.Sprintf(" %s%s %s", indent, iconWorktree, name)

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, wtStyle, w)
	}
	return wtStyle.Width(w).Render(label)
}

func (m *Model) renderSession(item listItem, selected bool, w int, dimAll bool, inFlash bool, isMatch bool, flashLabel rune) string {
	indent := strings.Repeat("  ", item.indent)
	title := "(session)"
	if item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated), item.session.Title)
	}
	if inFlash && isMatch && item.session != nil {
		title = fmt.Sprintf("%s  %s", TimeAgo(item.session.Updated),
			flashInlineLabel(item.session.Title, m.flashQuery, flashLabel))
	}

	// Truncate to prevent multiline wrapping.
	prefix := fmt.Sprintf(" %s%s ", indent, iconSession)
	maxTitle := w - len([]rune(prefix)) - 1
	if maxTitle > 0 {
		title = truncateStr(title, maxTitle)
	}
	label := prefix + title

	if dimAll || (inFlash && !isMatch) {
		return dimStyle.Width(w).Render(label)
	}
	if selected {
		return m.renderSelected(label, sessionStyle, w)
	}
	return sessionStyle.Width(w).Render(label)
}

// truncateStr truncates a string to maxLen runes, adding … if needed.
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "\u2026"
	}
	return string(runes[:maxLen-1]) + "\u2026"
}

// renderSelected renders a line with the amber ▌ selection bar.
func (m *Model) renderSelected(content string, base lipgloss.Style, w int) string {
	bar := accentBarStyle.Render("\u258c")
	// Render content with selected style, leave room for the bar.
	rest := selectedStyle.Width(w - 1).Render(content)
	return bar + rest
}

// padRight fills space between left content and right badges.
func (m *Model) padRight(left, right string, w int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := w - lw - rw - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) viewList() string {
	listW := 60
	if m.width > 80 {
		listW = 70
	}
	if m.width < 66 {
		listW = m.width - 6
	}

	var rows []string

	// Header — breadcrumb + position.
	inFlash := m.mode == viewFlash
	if inFlash {
		prefix := iconSearch
		if m.flashGlobal {
			prefix = iconSearch + " all"
		}
		searchLine := fmt.Sprintf(" %s %s\u2588", prefix, m.flashQuery)
		rows = append(rows, flashSearchStyle.Width(listW).Render(searchLine))
	} else {
		bc := m.breadcrumb()
		pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
		hdr := m.padRight(" "+bc, pos+" ", listW)
		rows = append(rows, headerStyle.Width(listW).Render(hdr))
	}

	// List items.
	rows = append(rows, m.renderListRows(listW, false)...)

	// Footer — minimal hints.
	if inFlash {
		matchInfo := fmt.Sprintf(" %d matches", len(m.flashMatches))
		hint := "letter to jump \u00b7 esc cancel"
		footer := m.padRight(matchInfo, hint+" ", listW)
		rows = append(rows, footerStyle.Width(listW).Render(footer))
	} else {
		hint := "\u23ce open   ? actions   s find   S all"
		rows = append(rows, footerStyle.Width(listW).Render(" "+hint))
	}

	panel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}

// --- which-key panel rendering ---

func (m *Model) viewWhichKey() string {
	listW := 48
	if m.width < 72 {
		listW = m.width - 28
		if listW < 30 {
			listW = 30
		}
	}

	// Render the list (dimmed).
	var rows []string
	bc := m.breadcrumb()
	pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
	hdr := m.padRight(" "+bc, pos+" ", listW)
	rows = append(rows, headerStyle.Width(listW).Render(hdr))
	rows = append(rows, m.renderListRows(listW, true)...)
	rows = append(rows, footerStyle.Width(listW).Render(" press a key or esc"))

	listPanel := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// Render the action panel.
	actions := m.whichKeyActions()
	title := m.whichKeyTitle()

	panelW := 20
	var actionLines []string
	actionLines = append(actionLines, whichKeyTitleStyle.Width(panelW-4).Render(title))
	actionLines = append(actionLines, "")

	for _, a := range actions {
		if a.key == "" {
			actionLines = append(actionLines, "")
			continue
		}
		keyPart := whichKeyKeyStyle.Render(a.key)
		descPart := whichKeyDescStyle.Render(" " + a.desc)
		actionLines = append(actionLines, " "+keyPart+descPart)
	}

	actionContent := strings.Join(actionLines, "\n")
	actionPanel := whichKeyBorderStyle.Width(panelW).Render(actionContent)

	// Position the action panel vertically aligned with the cursor.
	listH := lipgloss.Height(listPanel)
	panelH := lipgloss.Height(actionPanel)
	topPad := (listH - panelH) / 2
	if topPad < 0 {
		topPad = 0
	}
	paddedPanel := strings.Repeat("\n", topPad) + actionPanel

	combined := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, "  ", paddedPanel)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		combined,
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
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("%s New worktree for %s", iconWorktree, p.Name)))
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
		topicDisplay = m.wtTopic + "\u2588"
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

	// Field 1: branch override
	branchLabel := "  Branch:"
	branchDefault := fmt.Sprintf("wt/<machine>/%s", m.wtTopic)
	if m.wtTopic == "" && !hasBranch {
		branchDefault = "wt/<machine>/<topic>"
	}
	branchVal := m.wtBranch + "\u2588"
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
		pathPreview = fmt.Sprintf("  \u2192 dir: %s-wt-<machine>-%s", p.Name, layout.SlugifyBranch(m.wtBranch))
	} else if m.wtTopic != "" {
		pathPreview = fmt.Sprintf("  \u2192 dir: %s-wt-<machine>-%s", p.Name, m.wtTopic)
	}
	if pathPreview != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render(pathPreview))
	}
	lines = append(lines, "")

	// Field 2: auto-push toggle
	pushCheck := "\u2610"
	if m.wtAutoPush {
		pushCheck = "\u2611"
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
	confirmLabel := "  \u2192 Create worktree"
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
// Warm amber "command post" palette.

var (
	// Header / footer bars.
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("173")). // amber dim — breadcrumb
			Background(lipgloss.Color("235"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Background(lipgloss.Color("235"))

	// Selection: amber accent bar.
	accentBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")) // warm amber ▌

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")). // bright text
			Bold(true)

	// Type colors.
	groupStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("182")). // soft mauve
			Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")) // white — primary items

	wtStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("108")) // muted sage — git/branch

	sessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("110")) // cool steel — history

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")) // subtle

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Flash search.
	flashSearchStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")). // amber
				Background(lipgloss.Color("235"))

	flashLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("235")). // dark on amber
			Background(lipgloss.Color("215"))

	flashMatchStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("215")) // amber underlined match

	// Popup forms.
	popupBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(1, 1)

	popupTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("215")) // amber

	popupSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")).
				Background(lipgloss.Color("237"))

	popupItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254"))

	popupDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Which-key panel.
	whichKeyBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(0, 1)

	whichKeyTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")).
				Bold(true)

	whichKeyKeyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")). // amber key
				Bold(true)

	whichKeyDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")) // secondary text
)
