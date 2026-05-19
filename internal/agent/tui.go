package agent

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

type viewMode int

const (
	viewList viewMode = iota
	viewNewWorktree
	viewFlash
	viewPromptInput
	viewWhichKey
	viewEditProject
	viewChipAction
)

const (
	iconProject  = ""
	iconWorktree = ""
	iconSession  = ""
	iconSearch   = ""
)

type listItem struct {
	kind       NodeKind
	group      string
	project    *Project
	worktree   *Worktree
	session    *Session
	indent     int
	path       string
	parentProj *Project
}

type LaunchRequest struct {
	Cwd       string
	ResumeID  string
	ShellOnly bool
	Prompt    string
}

type Model struct {
	workspaces []WorkspaceData
	mode       viewMode
	items      []listItem
	cursor     int
	expanded   map[string]bool
	scroll     int

	headerChips []Chip

	chipTarget *Chip

	sessCache *SessionCache
	wtCache   *WorktreeCache

	statusMsg string

	pendingDelete bool
	deleteItem    *listItem

	popupProj *Project

	wtBranch   string
	wtNoLaunch bool
	wtField    int

	editGroup    string
	editCategory config.Category
	editField    int
	editErr      string

	pendingLaunch *LaunchRequest
	promptInput   string

	flashQuery    string
	flashMatches  []int
	flashLabels   []rune
	flashGlobal   bool
	savedExpanded map[string]bool

	whichKeyLevel int

	Launch *LaunchRequest

	width, height int
}

func NewModel(workspaces []WorkspaceData, sessCache *SessionCache) *Model {
	if sessCache == nil {
		sessCache = NewSessionCache()
	}
	m := &Model{
		workspaces: workspaces,
		mode:       viewList,
		expanded:   make(map[string]bool),
		sessCache:  sessCache,
		wtCache:    NewWorktreeCache(),
	}

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

		if msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			return m, tea.Quit
		}

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
		if m.mode == viewChipAction {
			return m.updateChipAction(msg)
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
	if m.mode == viewChipAction {
		return m.viewChipAction()
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
	chrome := 5
	if len(m.headerChips) > 0 {
		chrome += 3
	}
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	return h
}

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
