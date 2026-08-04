package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
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
	kind          NodeKind
	workspaceRoot string
	group         string
	project       *Project
	indent        int
	path          string
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

	popupProj *Project

	wtBranch tui.TextInput
	wtField  int

	editGroup    tui.TextInput
	editCategory config.Category
	editField    int
	editErr      string

	flashQuery    tui.TextInput
	flashMatches  []int
	flashLabels   []rune
	flashGlobal   bool
	savedExpanded map[string]bool

	whichKeyLevel int

	Launch *LaunchRequest

	width, height int
}

func NewModel(workspaces []WorkspaceData) *Model {
	wtBranch := tui.NewTextInput()
	wtBranch.SetPrompt("")
	editGroup := tui.NewTextInput()
	editGroup.SetPrompt("")
	flashQuery := tui.NewTextInput()
	flashQuery.SetPrompt("")
	m := &Model{
		workspaces: workspaces,
		mode:       viewList,
		expanded:   make(map[string]bool),
		wtCache:    NewWorktreeCache(),
		wtBranch:   wtBranch,
		editGroup:  editGroup,
		flashQuery: flashQuery,
	}

	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			m.expanded[groupKey(ws.Root, g)] = true
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
				return m.launch(item.workspaceRoot, item.path)
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
	if proj != nil && proj.WorkspaceRoot != "" {
		return proj.WorkspaceRoot
	}
	for _, ws := range m.workspaces {
		for _, p := range ws.Projects {
			if p.Path == proj.Path {
				return ws.Root
			}
		}
	}
	return ""
}

func (m *Model) launch(workspaceRoot, path string) (tui.Model, tui.Cmd) {
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		m.statusMsg = "shell: " + err.Error()
		return m, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		m.statusMsg = "shell: " + err.Error()
		return m, nil
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		m.statusMsg = fmt.Sprintf("shell: path is outside workspace %s", filepath.Base(workspaceRoot))
		return m, nil
	}
	m.Launch = &LaunchRequest{Cwd: path}
	return m, tui.Quit
}

func (m *Model) toggleExpand(key string) {
	m.expanded[key] = !m.expanded[key]
	m.rebuildItems()
	m.ensureVisible()
}

func (m *Model) jumpToGroup(workspaceRoot, group string) {
	for i, it := range m.items {
		if it.kind == KindGroup && it.workspaceRoot == workspaceRoot && it.group == group {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

func (m *Model) jumpToProject(workspaceRoot, projID string) {
	for i, it := range m.items {
		if it.kind == KindProject && it.workspaceRoot == workspaceRoot && it.project != nil && it.project.ID == projID {
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
	}
	return "ws"
}

func (m *Model) updateList(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	m.statusMsg = ""
	item := m.currentItem()

	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < len(m.headerChips) {
			chip := m.headerChips[idx]
			return m.launch(chip.WorkspaceRoot, chip.Path)
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
			m.sheet = newGroupSheet(m, item.workspaceRoot, item.group)
			return m, nil
		case KindProject:
			m.sheet = newProjectSheet(m, item.project, nil)
			return m, nil
		}

	case "w":

		if item != nil && item.kind == KindProject {
			m.wtBranch.SetValue("")
			m.wtBranch.Focus()
			m.wtField = 0
			m.popupProj = item.project
			m.mode = viewNewWorktree
			return m, nil
		}

	case "e":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.popupProj = item.project
			m.editGroup.SetValue(item.project.Group)
			m.editGroup.Focus()
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
			return m.launch(item.workspaceRoot, item.path)
		}

	case "f":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" {
			m.toggleFavoriteGroup(item.workspaceRoot, item.group)
		}

	case "h", "left":
		if item != nil {
			switch {
			case item.kind == KindProject && item.project.Group != "":
				key := groupKey(item.workspaceRoot, item.project.Group)
				m.expanded[key] = false
				m.rebuildItems()
				m.jumpToGroup(item.workspaceRoot, item.project.Group)
			case item.kind == KindGroup && m.expanded[groupKey(item.workspaceRoot, item.group)]:
				m.expanded[groupKey(item.workspaceRoot, item.group)] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "tab":

		if item != nil && item.kind == KindGroup {
			m.toggleExpand(groupKey(item.workspaceRoot, item.group))
		}

	case "s", "/":
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashQuery.SetValue("")
		m.flashQuery.Focus()
		m.recomputeFlash()

	case "S":

		m.flashGlobal = true
		m.savedExpanded = make(map[string]bool)
		for k, v := range m.expanded {
			m.savedExpanded[k] = v
		}

		for _, ws := range m.workspaces {
			for _, g := range ws.Groups {
				m.expanded[groupKey(ws.Root, g)] = true
			}
			for i := range ws.Projects {
				m.expanded["proj:"+ws.Projects[i].ID] = true
			}
		}
		m.rebuildItems()
		m.mode = viewFlash
		m.flashQuery.SetValue("")
		m.flashQuery.Focus()
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
