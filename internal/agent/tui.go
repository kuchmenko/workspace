package agent

import (
	"fmt"
	"log"
	"os"
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
	viewLifecycle
	viewJobs
)

const (
	iconProject  = ""
	iconWorktree = ""
	iconSearch   = ""
)

type listItem struct {
	kind            NodeKind
	workspaceRoot   string
	group           string
	project         *Project
	indent          int
	path            string
	expandKey       string
	projectionGroup bool
	worktree        *Worktree
	parentProj      *Project
}

type LaunchRequest struct {
	Cwd string
}

type Model struct {
	workspaces  []WorkspaceData
	mode        viewMode
	items       []listItem
	cursor      int
	expanded    map[string]bool
	scroll      int
	homeView    string
	recentOrder string
	savedItems  []listItem
	savedCursor int
	savedScroll int

	headerChips []Chip

	sheet *sheet

	wtCache *WorktreeCache

	statusMsg string

	popupProj       *Project
	formReturnSheet *sheet
	formReturnFlash *flashRefreshState

	wtBranch tui.TextInput
	wtField  int

	editGroup    tui.TextInput
	editCategory config.Category
	editField    int
	editErr      string

	flashQuery       tui.TextInput
	flashMatches     []int
	flashLabels      []rune
	flashGlobal      bool
	flashEditing     bool
	flashReturnSheet *sheet
	savedExpanded    map[string]bool

	paletteQuery         tui.TextInput
	paletteCursor        int
	paletteOrigin        *paletteOrigin
	lifecycle            *lifecycleModel
	lifecycleJob         *lifecycleModel
	lifecycleReturnFlash *flashRefreshState
	jobsRunner           *operationRunner
	jobs                 []*explorerJob
	jobsCursor           int
	jobsSelectedID       string
	jobsDetail           bool
	jobsDetailScroll     int
	activitySearch       bool
	activityEditing      bool
	activityOriginID     string
	activityQuery        tui.TextInput
	activityMatches      []string
	jobsReturnSheet      *sheet
	activityReturnFlash  *flashRefreshState
	debugLog             *log.Logger
	debugLogFile         *os.File
	debugLogPath         string

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
	paletteQuery := tui.NewTextInput()
	paletteQuery.SetPrompt("")
	m := &Model{
		workspaces:    workspaces,
		mode:          viewList,
		expanded:      make(map[string]bool),
		wtCache:       NewWorktreeCache(),
		wtBranch:      wtBranch,
		editGroup:     editGroup,
		flashQuery:    flashQuery,
		paletteQuery:  paletteQuery,
		activityQuery: tui.NewTextInput(),
		jobsRunner:    newOperationRunner(),
		homeView:      config.ExplorerViewRecent,
		recentOrder:   config.RecentOrderDesc,
	}
	if mc, err := config.LoadMachineConfig(); err == nil {
		m.homeView, m.recentOrder = mc.ExplorerView, mc.RecentOrder
	}
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			p := &m.workspaces[wi].Projects[pi]
			m.wtCache.SeedInventory(p.Path, p.WorktreeInventory)
		}
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
		m.ensureVisible()
		if m.jobsDetail {
			m.jobsDetailScroll = min(m.jobsDetailScroll, max(0, len(m.activityDetailLines())-max(1, m.height-2)))
		}
		if m.lifecycle != nil {
			m.lifecycle.scroll = min(m.lifecycle.scroll, max(0, len(m.lifecycleBody())-m.lifecycleBodyRows()))
		}
		return m, nil
	case lifecycleProgressMsg:
		return m.updateLifecycleProgress(msg)
	case lifecycleDoneMsg:
		return m.finishLifecycleJob(msg)
	case lifecyclePlanDoneMsg:
		return m.finishLifecyclePlan(msg)
	case lifecycleRefreshDoneMsg:
		return m.finishLifecycleRefresh(msg)
	case jobStreamMsg:
		return m, waitJobStream(msg)
	case waitJobStreamMsg:
		cmd := waitJobStream(jobStreamMsg{msg.runner, msg.id, msg.events})
		if event, ok := msg.event.(jobEvent); ok {
			m.applyJobEvent(event)
		}
		return m, cmd

	case tui.KeyMsg:
		if msg.String() == "ctrl+o" {
			if m.mode == viewWhichKey {
				m.closePalette()
				return m, nil
			}
			return m, m.openPalette()
		}
		if m.mode == viewFlash {
			return m.updateFlash(msg)
		}
		if m.mode == viewWhichKey {
			return m.updateWhichKey(msg)
		}

		if msg.String() == "ctrl+c" || msg.String() == "ctrl+q" {
			if m.jobsRunning() {
				m.statusMsg = "actions are still queued or running · A Open Activity"
				return m, nil
			}
			return m, tui.Quit
		}

		if msg.String() == "ctrl+s" {
			if m.mode == viewList {
				if m.sheet != nil {
					return m.launch(m.sheet.workspaceRootForTarget(), m.sheet.primaryPath())
				}
				item := m.currentItem()
				if item != nil && item.path != "" {
					return m.launch(item.workspaceRoot, item.path)
				}
			}
			return m, nil
		}
		if m.mode == viewNewWorktree {
			return m.updateNewWorktree(msg)
		}
		if m.mode == viewEditProject {
			return m.updateEditProject(msg)
		}
		if m.mode == viewLifecycle {
			return m.updateLifecycle(msg)
		}
		if m.mode == viewJobs {
			return m.updateJobs(msg)
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
	if m.mode == viewLifecycle {
		return m.viewLifecycle()
	}
	if m.mode == viewJobs {
		return m.viewJobs()
	}
	if m.mode == viewWhichKey {
		return m.viewWhichKey()
	}
	if m.sheet != nil {
		return m.sheet.view(m)
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
	if m.jobsRunning() {
		m.statusMsg = "actions are still queued or running · A Open Activity"
		return m, nil
	}
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
	chrome := 3
	if len(m.jobs) > 0 {
		chrome++
	}
	chrome += len(m.quickSlotLines(max(1, m.width)))
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) footerHints() (actions, nav string) {
	nav = "j/k:move  g/G:first/last  ^d/^u:half  ^f/^b:page  ?:more"
	item := m.currentItem()
	if item == nil {
		return "⏎:open  s:find  S:all", nav
	}
	switch item.kind {
	case KindGroup:
		if item.projectionGroup {
			actions = "⏎/l:open  h:back  tab:expand"
		} else {
			actions = "⏎/l:open  h:back  f:favorite  tab:expand  A:Activity  M:maintenance  S:search"
		}
	case KindProject:
		actions = "⏎/l:open  h:back  w:new  e:edit  f:favorite  A:Activity  M:maintenance  S:search"
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
		return m.openQuickSlot(idx)
	}

	switch msg.String() {
	case "q":
		if m.jobsRunning() {
			m.statusMsg = "actions are still queued or running · A Open Activity"
			return m, nil
		}
		return m, tui.Quit
	case "v":
		switch m.homeView {
		case config.ExplorerViewRecent:
			m.homeView = config.ExplorerViewProjects
		case config.ExplorerViewProjects:
			m.homeView = config.ExplorerViewLanguage
		default:
			m.homeView = config.ExplorerViewRecent
		}
		m.saveExplorerPreferences()
		m.rebuildItems()
		m.cursor, m.scroll = 0, 0
	case "o":
		if m.homeView == config.ExplorerViewRecent {
			if m.recentOrder == config.RecentOrderDesc {
				m.recentOrder = config.RecentOrderAsc
			} else {
				m.recentOrder = config.RecentOrderDesc
			}
			m.saveExplorerPreferences()
			m.rebuildItems()
		}
	case "A":
		m.openActivity(nil)
		return m, nil
	case "M":
		m.openLifecycle(lifecycleScope{kind: lifecycleGlobal})
		return m, nil
	case "j", "down":
		m.moveHomeCursor(1)
	case "k", "up":
		m.moveHomeCursor(-1)
	case "ctrl+d":
		m.moveHomeCursor(max(1, m.listHeight()/2))
	case "ctrl+u":
		m.moveHomeCursor(-max(1, m.listHeight()/2))
	case "ctrl+f", "pgdn":
		m.moveHomeCursor(m.listHeight())
	case "ctrl+b", "pgup":
		m.moveHomeCursor(-m.listHeight())
	case "enter", "l", "right":
		return m.openCurrentItem()

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

	case "f":

		if item != nil && item.kind == KindProject && item.project != nil {
			return m, m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" && !item.projectionGroup {
			return m, m.toggleFavoriteGroup(item.workspaceRoot, item.group)
		}

	case "h", "left":
		if item != nil {
			switch {
			case item.kind == KindProject && item.expandKey != "":
				m.expanded[item.expandKey] = false
				m.rebuildItems()
				m.jumpToExpandKey(item.expandKey)
			case item.kind == KindGroup && m.expanded[item.expandKey]:
				m.expanded[item.expandKey] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "tab":

		if item != nil && item.kind == KindGroup {
			m.toggleExpand(item.expandKey)
		}

	case "s", "/":
		m.savedCursor, m.savedScroll = m.cursor, m.scroll
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashEditing = true
		m.flashQuery.SetValue("")
		m.flashQuery.Focus()
		m.recomputeFlash()

	case "S":
		m.openGlobalSearch()

	case "G", "end":
		m.moveHomeTo(len(m.items) - 1)
	case "g", "home":
		m.moveHomeTo(0)
	}
	return m, nil
}

func (m *Model) openQuickSlot(index int) (tui.Model, tui.Cmd) {
	if index < 0 || index >= len(m.headerChips) || m.headerChips[index].Project == nil {
		return m, nil
	}
	m.sheet = newProjectSheet(m, m.headerChips[index].Project, nil)
	return m, nil
}

func (m *Model) saveExplorerPreferences() {
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return
	}
	mc.ExplorerView, mc.RecentOrder = m.homeView, m.recentOrder
	_ = config.SaveMachineConfig(mc)
}
