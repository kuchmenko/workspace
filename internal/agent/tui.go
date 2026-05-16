package agent

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

// View mode.
type viewMode int

const (
	viewList        viewMode = iota // nested list — all navigation lives here
	viewNewWorktree                 // worktree creation form
	viewFlash                       // flash search with jump labels
	viewPromptInput                 // optional prompt input before launching claude
	viewWhichKey                    // which-key action panel (? or space)
	viewEditProject                 // edit project group/category
)

// Nerd Font icons.
const (
	iconProject  = "" //  nf-oct-package
	iconWorktree = "" //  nf-dev-git_branch
	iconSession  = "" //  nf-md-message_text_outline
	iconSearch   = "" //  nf-fa-search
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

	// sectionTitle is the text rendered for KindSection rows. Empty
	// sectionTitle on a KindSection row renders as a blank line —
	// used as the spacer between Favorites/Recent and the full tree.
	sectionTitle string
	// inHeader marks a project row that lives inside the Favorites or
	// Recent header section rather than the full workspace tree. Such
	// rows skip worktree/session expansion (they are quick-nav only).
	inHeader bool
}

// isSelectable reports whether the cursor is allowed to land on this
// row. KindSection rows are visual-only and skipped by j/k movement,
// flash-search match collection, and the initial-cursor placement.
func (it listItem) isSelectable() bool {
	return it.kind != KindSection
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
	// agentView is "all" (Favorites+Recent header above full tree) or
	// "favorites" (only the Favorites section, flat — no tree). Loaded
	// from workspace.toml's [agent].default_view at startup. The user
	// flips it via the which-key `space v` chord; the new value is
	// persisted back to workspace.toml so other machines pick it up.
	agentView string
	items     []listItem // flattened visible items
	cursor    int
	expanded  map[string]bool // group/project name → expanded
	scroll    int             // scroll offset for long lists

	// Caches — loaded lazily, invalidated after mutations.
	sessCache *SessionCache
	wtCache   *WorktreeCache

	// Status message — shown in footer until next keypress.
	statusMsg string

	// Delete confirmation state.
	pendingDelete bool // true = waiting for y/n confirmation
	deleteItem    *listItem

	// Active project for the worktree-creation form.
	popupProj *Project

	// Worktree creation form state.
	wtBranch   string // user-typed branch name (no prefix injection)
	wtNoLaunch bool   // true when "create only", false when "create + launch"
	wtField    int    // 0=branch, 1=confirm

	// Edit-project form state.
	editGroup    string
	editCategory config.Category
	editField    int // 0=group, 1=category, 2=save
	editErr      string

	// Prompt input state (optional prompt before launch).
	pendingLaunch *LaunchRequest // set before entering prompt input
	promptInput   string

	// Flash search state.
	flashQuery    string
	flashMatches  []int           // indices into m.items that match
	flashLabels   []rune          // one label per match (a, b, c, ...)
	flashGlobal   bool            // S = global search (all items, even collapsed)
	savedExpanded map[string]bool // expansion state before global flash

	// Which-key state.
	whichKeyLevel int // 0 = root actions, 1 = worktree sub-menu

	// Set when the user picks a launch action.
	Launch *LaunchRequest

	width, height int
}

// NewModel constructs the TUI model from loaded workspace data.
// sessCache should be the cache returned by LoadWorkspaces (already
// populated with session counts from the initial scan).
//
// initialView selects the opening view ("all" or "favorites"). The
// caller typically reads it from workspace.toml's agent.default_view
// via Workspace.AgentDefaultView(); pass the empty string to default
// to "all".
func NewModel(workspaces []WorkspaceData, sessCache *SessionCache, initialView string) *Model {
	if sessCache == nil {
		sessCache = NewSessionCache()
	}
	view := config.AgentViewAll
	if initialView == config.AgentViewFavorites {
		view = config.AgentViewFavorites
	}
	m := &Model{
		workspaces: workspaces,
		mode:       viewList,
		agentView:  view,
		expanded:   make(map[string]bool),
		sessCache:  sessCache,
		wtCache:    NewWorktreeCache(),
	}
	// Auto-expand all groups initially.
	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			m.expanded[g] = true
		}
	}
	m.rebuildItems()
	m.cursor = m.firstSelectableIndex()
	return m
}

// firstSelectableIndex returns the index of the first row the cursor
// can legally land on. Used to skip past the Favorites/Recent section
// headers at startup. Returns 0 when nothing is selectable (degenerate
// empty workspace) so the cursor still has a defined value.
func (m *Model) firstSelectableIndex() int {
	for i, it := range m.items {
		if it.isSelectable() {
			return i
		}
	}
	return 0
}

// nextSelectable steps from `from` in direction `dir` (+1 down, -1 up)
// over any KindSection rows until it lands on a selectable row.
// Returns `from` when no selectable row exists in that direction —
// callers use the no-change signal to skip the ensureVisible call.
func (m *Model) nextSelectable(from, dir int) int {
	if len(m.items) == 0 {
		return from
	}
	i := from + dir
	for i >= 0 && i < len(m.items) {
		if m.items[i].isSelectable() {
			return i
		}
		i += dir
	}
	return from
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
		if m.mode == viewNewWorktree {
			return m.updateNewWorktree(msg)
		}
		if m.mode == viewEditProject {
			return m.updateEditProject(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == viewPromptInput {
		return m.viewPromptInput()
	}
	if m.mode == viewNewWorktree {
		return m.viewNewWorktree()
	}
	if m.mode == viewEditProject {
		return m.viewEditProject()
	}
	if m.mode == viewWhichKey {
		return m.viewWhichKey()
	}
	return m.viewList()
}

func (m *Model) currentItem() *listItem {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		return &m.items[m.cursor]
	}
	return nil
}

// workspaceRootFor returns the workspace root directory for a project.
// Matches by Path (globally unique) rather than ID (per-workspace key).
func (m *Model) workspaceRootFor(proj *Project) string {
	for _, ws := range m.workspaces {
		for _, p := range ws.Projects {
			if p.Path == proj.Path {
				return ws.Root
			}
		}
	}
	return ""
}

// primaryWorkspaceRoot returns the root used for workspace-wide
// settings (currently just `agent.default_view`). The TUI displays
// at most one workspace at a time in practice; when multiple are
// registered we pick the first deterministically — the user has only
// one global preference per session and `loadAgentDefaultView` reads
// from the same workspace, so the round-trip is consistent.
func (m *Model) primaryWorkspaceRoot() string {
	if len(m.workspaces) == 0 {
		return ""
	}
	return m.workspaces[0].Root
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
	h := m.height - 5 // header + 2 footer lines + borders
	if h < 3 {
		h = 3
	}
	return h
}

// footerHints returns two lines of context-sensitive keyboard hints.
// Line 1: actions available for the currently selected item type.
// Line 2: universal navigation shortcuts.
func (m *Model) footerHints() (actions, nav string) {
	nav = "j/k:↕  tab:expand  s:find  S:all  ?:more"
	item := m.currentItem()
	if item == nil {
		return "⏎:open  s:find  S:all", nav
	}
	switch item.kind {
	case KindGroup:
		actions = "⏎:claude  p:+prompt  l:shell"
	case KindProject:
		actions = "⏎:claude  p:+prompt  w:worktree  e:edit  l:shell"
	case KindWorktree:
		if item.worktree != nil && !item.worktree.IsMain {
			actions = "⏎:claude  p:+prompt  l:shell  m:promote  d:delete"
		} else {
			actions = "⏎:claude  p:+prompt  l:shell"
		}
	case KindPortal:
		actions = "⏎:resume  p:+prompt"
	default:
		actions = "⏎:open"
	}
	return actions, nav
}

// breadcrumb derives contextual header from the current cursor position.
func (m *Model) breadcrumb() string {
	item := m.currentItem()
	if item == nil {
		return "ws"
	}
	switch item.kind {
	case KindGroup:
		return item.group + " ›"
	case KindProject:
		if item.project.Group != "" {
			return item.project.Group + " ›"
		}
		return "ws"
	case KindWorktree, KindPortal:
		if item.parentProj != nil {
			if item.parentProj.Group != "" {
				return item.parentProj.Group + " › " + item.parentProj.Name
			}
			return item.parentProj.Name
		}
		return "ws"
	}
	return "ws"
}
