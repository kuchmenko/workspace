package agent

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/tui"
)

type viewMode int

const (
	viewList viewMode = iota
	viewNewWorktree
	viewFlash
	viewWhichKey
	viewEditProject
)

const (
	iconProject  = ""
	iconWorktree = ""
	iconSearch   = ""
)

type listItem struct {
	kind       NodeKind
	group      string
	project    *Project
	worktree   *Worktree
	indent     int
	path       string
	parentProj *Project
}

type LaunchRequest struct {
	Cwd string
}

type Model struct {
	workspaces []WorkspaceData
	mode       viewMode
	items      []listItem
	cursor     int
	expanded   map[string]bool
	scroll     int

	headerChips []Chip

	sheet *sheet

	wtCache *WorktreeCache

	statusMsg string

	pendingDelete bool
	deleteItem    *listItem

	popupProj *Project

	wtBranch string
	wtField  int

	editGroup    string
	editCategory config.Category
	editField    int
	editErr      string

	flashQuery    string
	flashMatches  []int
	flashLabels   []rune
	flashGlobal   bool
	savedExpanded map[string]bool

	whichKeyLevel int

	Launch *LaunchRequest

	width, height int
}

func NewModel(workspaces []WorkspaceData) *Model {
	m := &Model{
		workspaces: workspaces,
		mode:       viewList,
		expanded:   make(map[string]bool),
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

func (m *Model) Init() tui.Cmd { return nil }

func (m *Model) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tui.KeyMsg:

		if msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			return m, tui.Quit
		}

		if msg.String() == "ctrl+s" {
			item := m.currentItem()
			if item != nil && item.path != "" {
				m.Launch = &LaunchRequest{Cwd: item.path}
				return m, tui.Quit
			}
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
		if m.sheet != nil {
			return m.sheet.update(m, msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.mode == viewNewWorktree {
		return m.viewNewWorktree()
	}
	if m.mode == viewEditProject {
		return m.viewEditProject()
	}
	if m.sheet != nil {
		return m.sheet.view(m.width, m.height)
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
	nav = "j/k:↕  1-9:chip  s:find  S:all  ?:more"
	item := m.currentItem()
	if item == nil {
		return "⏎:open  s:find  S:all", nav
	}
	switch item.kind {
	case KindGroup:
		actions = "⏎:sheet  tab:expand  l:shell"
	case KindProject:
		actions = "⏎:sheet  w:worktree  e:edit  l:shell"
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
	case KindWorktree:
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

func (m *Model) updateList(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	if m.pendingDelete {
		m.pendingDelete = false
		if msg.String() == "y" && m.deleteItem != nil {
			it := m.deleteItem
			m.deleteItem = nil

			projID := ""
			if it.parentProj != nil {
				projID = it.parentProj.ID
			}
			wsRoot := m.workspaceRootFor(it.parentProj)
			machine, err := explorerMachineName()
			if err == nil {
				err = repo.RemoveWorktree(repo.WorktreeRemoveOptions{WorkspaceRoot: wsRoot, Project: projID, Branch: it.worktree.Branch, Machine: machine})
			}
			if err != nil {
				m.statusMsg = err.Error()
				return m, nil
			}
			m.wtCache.Invalidate(it.parentProj.Path)
			m.rebuildItems()
			m.ensureVisible()
			m.statusMsg = "worktree deleted"
			return m, nil
		}
		m.deleteItem = nil
		m.statusMsg = ""
		return m, nil
	}

	m.statusMsg = ""
	item := m.currentItem()

	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < len(m.headerChips) {
			m.Launch = &LaunchRequest{Cwd: m.headerChips[idx].Path}
			return m, tui.Quit
		}
	}

	switch msg.String() {
	case "q":
		return m, tui.Quit
	case "j", "down":
		if m.cursor+1 < len(m.items) {
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
			m.sheet = newGroupSheet(m, item.group)
			return m, nil
		case KindProject:
			m.sheet = newProjectSheet(m, item.project, nil)
			return m, nil
		case KindWorktree:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tui.Quit
		}

	case "w":

		if item != nil && item.kind == KindProject {
			m.wtBranch = ""
			m.wtField = 0
			m.popupProj = item.project
			m.mode = viewNewWorktree
			return m, nil
		}

	case "e":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.popupProj = item.project
			m.editGroup = item.project.Group
			m.editCategory = config.Category(item.project.Category)
			if m.editCategory == "" {
				m.editCategory = config.CategoryPersonal
			}
			m.editField = 0
			m.editErr = ""
			m.mode = viewEditProject
			return m, nil
		}

	case "l", "right":
		if item != nil && item.path != "" {
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tui.Quit
		}

	case "f":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" {
			m.toggleFavoriteGroup(item.group)
		}

	case "h", "left":
		if item != nil {
			switch {
			case item.kind == KindProject && item.project.Group != "":
				m.expanded[item.project.Group] = false
				m.rebuildItems()
				m.jumpToGroup(item.project.Group)
			case item.kind == KindGroup && m.expanded[item.group]:
				m.expanded[item.group] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "tab":

		if item != nil && item.kind == KindGroup {
			m.toggleExpand(item.group)
		}

	case "d":
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			wt := item.worktree
			if git.IsDirty(wt.Path) {
				m.statusMsg = "cannot delete: uncommitted changes"
				break
			}
			ahead, _, hasUpstream := git.AheadBehind(wt.Path, wt.Branch)
			if hasUpstream && ahead > 0 {
				m.statusMsg = fmt.Sprintf("cannot delete: %d unpushed commit(s)", ahead)
				break
			}

			name := worktreeDisplayName(*wt)
			m.statusMsg = fmt.Sprintf("delete %s? y to confirm", name)
			m.pendingDelete = true
			m.deleteItem = item
		}

	case "s", "/":
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "S":

		m.flashGlobal = true
		m.savedExpanded = make(map[string]bool)
		for k, v := range m.expanded {
			m.savedExpanded[k] = v
		}

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
