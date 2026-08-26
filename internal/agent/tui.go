package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/runner"
	"github.com/kuchmenko/workspace/internal/tui"
)

type viewMode int

const (
	viewList viewMode = iota
	viewNewWorktree
	viewFlash
	viewWhichKey
	viewEditProject
	viewAlias
	viewLifecycle
	viewJobs
	viewRunners
	viewRunnerForm
	viewRunnerPrefix
	viewRunnerConfirm
)

const (
	iconWorktree = ""
	iconSearch   = ""
)

type listItem struct {
	kind            NodeKind
	workspaceRoot   string
	workspaceName   string
	group           string
	project         *Project
	indent          int
	count           int
	path            string
	expandKey       string
	parentExpandKey string
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
	aliasInput   tui.TextInput
	aliasTarget  explorerAliasTarget
	aliasError   string

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
	jobsReturnSheet      *sheet
	activityReturnFlash  *flashRefreshState
	runnerInfos          []runner.Info
	runnerCursor         int
	runnerID             tui.TextInput
	runnerPrefix         tui.TextInput
	runnerPrefixError    string
	runnerForm           *runnerForm
	runnerConfirm        *runnerConfirmation
	runnerReturnMode     viewMode
	runnerReturnSheet    *sheet
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
	aliasInput := tui.NewTextInput()
	aliasInput.SetPrompt("")
	flashQuery := tui.NewTextInput()
	flashQuery.SetPrompt("")
	paletteQuery := tui.NewTextInput()
	paletteQuery.SetPrompt("")
	runnerID := tui.NewTextInput()
	runnerID.SetPrompt("")
	runnerPrefix := tui.NewTextInput()
	runnerPrefix.SetPrompt("")
	m := &Model{
		workspaces:    workspaces,
		mode:          viewList,
		expanded:      make(map[string]bool),
		wtCache:       NewWorktreeCache(),
		wtBranch:      wtBranch,
		editGroup:     editGroup,
		aliasInput:    aliasInput,
		flashQuery:    flashQuery,
		paletteQuery:  paletteQuery,
		runnerID:      runnerID,
		runnerPrefix:  runnerPrefix,
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
		m.expanded[workspaceKey(ws.Root)] = true
	}
	m.rebuildItems()
	return m
}

func (m *Model) Init() tui.Cmd { return refreshRunners() }

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
	case runnerRefreshMsg:
		m.applyRunnerRefresh(msg)
		return m, runnerRefreshTick()
	case runnerRefreshTickMsg:
		return m, refreshRunners()

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
		if m.mode == viewAlias {
			return m.updateAlias(msg)
		}
		if m.mode == viewLifecycle {
			return m.updateLifecycle(msg)
		}
		if m.mode == viewJobs {
			return m.updateJobs(msg)
		}
		if m.mode == viewRunners {
			return m.updateRunners(msg)
		}
		if m.mode == viewRunnerForm {
			return m.updateRunnerForm(msg)
		}
		if m.mode == viewRunnerPrefix {
			return m.updateRunnerPrefix(msg)
		}
		if m.mode == viewRunnerConfirm {
			return m.updateRunnerConfirm(msg)
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
	if m.mode == viewAlias {
		return m.viewAlias()
	}
	if m.mode == viewLifecycle {
		return m.viewLifecycle()
	}
	if m.mode == viewJobs {
		return m.viewJobs()
	}
	if m.mode == viewRunners {
		return m.viewRunners()
	}
	if m.mode == viewRunnerForm {
		return m.viewRunnerForm()
	}
	if m.mode == viewRunnerPrefix {
		return m.viewRunnerPrefix()
	}
	if m.mode == viewRunnerConfirm {
		return m.viewRunnerConfirm()
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
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	return h
}
