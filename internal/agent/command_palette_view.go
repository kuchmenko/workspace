package agent

import (
	"fmt"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) paletteFavoriteLabel(project *Project) string {
	if project.Favorite {
		return "Remove favorite"
	}
	return "Add favorite"
}

func (m *Model) paletteTitle() string {
	switch m.mode {
	case viewNewWorktree:
		if m.popupProj != nil {
			return "Commands · New worktree · " + presentLabel(m.popupProj.Name)
		}
		return "Commands · New worktree"
	case viewEditProject:
		if m.popupProj != nil {
			return "Commands · Edit project · " + presentLabel(m.popupProj.Name)
		}
		return "Commands · Edit project"
	case viewAlias:
		return "Commands · Edit alias · " + presentLabel(m.aliasTarget.label)
	case viewLifecycle:
		return "Commands · " + m.lifecycleScopeLabel() + " · " + m.lifecyclePhaseLabel()
	case viewJobs:
		if m.jobsSelectedID != "" {
			return "Commands · Activity " + m.jobsSelectedID
		}
		return "Commands · Activity"
	case viewRunners:
		if info := m.selectedRunner(); info != nil {
			name := info.Definition.ID
			if name == "" {
				name = filepath.Base(info.Path)
			}
			return "Commands · Amp runners · " + name
		}
		return "Commands · Amp runners"
	case viewRunnerForm:
		return "Commands · Create Amp runner"
	case viewRunnerPrefix:
		return "Commands · Amp runner settings"
	case viewFlash:
		if item := m.currentItem(); item != nil {
			return "Commands · Search · " + presentLabel(m.itemSearchName(*item))
		}
		return "Commands · Search"
	}
	if m.sheet != nil {
		if selected := m.sheet.visualWorktrees(); len(selected) > 0 {
			return fmt.Sprintf("Commands · %d worktrees", len(selected))
		}
		if row := m.sheet.focused(); row != nil {
			if row.wt != nil {
				return "Commands · " + presentLabel(worktreeDisplayName(*row.wt))
			}
			if row.proj != nil {
				return "Commands · " + presentLabel(row.proj.Name)
			}
		}
	}
	if item := m.currentItem(); item != nil {
		if item.kind == KindProject && item.project != nil {
			return "Commands · " + presentLabel(item.project.Name)
		}
		if item.kind == KindGroup {
			return "Commands · @" + presentLabel(item.group)
		}
	}
	return "Commands · Home"
}

func (m *Model) viewWhichKey() string {
	commands := m.filteredPaletteCommands()
	width := min(88, max(30, m.width-4))
	if m.width < 76 {
		width = max(1, m.width)
	}
	height := min(max(8, m.height-2), max(10, m.height*2/3))
	if m.width < 76 {
		height = max(1, m.height-1)
	}
	title := "Commands"
	if m.paletteOrigin != nil {
		title = m.paletteOrigin.title
	}
	rows := []string{whichKeyTitleStyle.Render(title), flashSearchStyle.Width(max(1, width-4)).Render("> " + m.paletteQuery.View()), ""}
	contentHeight := max(1, height-2)
	commandLines := make([]string, 0, len(commands))
	selectedLine, lastGroup := 0, ""
	for i, command := range commands {
		if command.group != lastGroup {
			if lastGroup != "" {
				commandLines = append(commandLines, "")
			}
			commandLines = append(commandLines, dimStyle.Render(command.group))
			lastGroup = command.group
		}
		line := padPanelRight("  "+command.name, command.key, max(1, width-4))
		if i == m.paletteCursor {
			line = "▌" + selectedStyle.Width(max(1, width-5)).Render(tui.Truncate(line[1:], max(1, width-5)))
			selectedLine = len(commandLines)
		}
		commandLines = append(commandLines, line)
	}
	visibleRows := max(1, contentHeight-4)
	start, end := tui.WindowAround(selectedLine, len(commandLines), visibleRows)
	rows = append(rows, commandLines[start:end]...)
	for len(rows) < contentHeight-1 {
		rows = append(rows, "")
	}
	rows = append(rows, dimStyle.Render("Enter open · j/k move · Ctrl+O/q close"))
	panel := whichKeyBorderStyle.Width(width - 2).Render(tui.JoinVertical(tui.Left, rows...))
	return tui.Overlay(tui.DimCanvas(m.width, m.height, m.paletteBackgroundView()), panel, m.width, m.height)
}

func (m *Model) paletteBackgroundView() string {
	mode := m.mode
	if m.paletteOrigin != nil {
		m.mode = m.paletteOrigin.mode
	}
	defer func() { m.mode = mode }()
	switch m.mode {
	case viewNewWorktree:
		return m.viewNewWorktree()
	case viewEditProject:
		return m.viewEditProject()
	case viewAlias:
		return m.viewAlias()
	case viewLifecycle:
		return m.viewLifecycle()
	case viewJobs:
		return m.viewJobs()
	case viewRunners:
		return m.viewRunners()
	case viewRunnerForm:
		return m.viewRunnerForm()
	case viewRunnerPrefix:
		return m.viewRunnerPrefix()
	case viewRunnerConfirm:
		return m.viewRunnerConfirm()
	}
	if m.sheet != nil {
		return m.sheet.view(m)
	}
	return m.viewList()
}

func (m *Model) reconcilePaletteAfterRefresh() {
	if m.paletteOrigin == nil {
		return
	}
	originalSheet := m.paletteOrigin.sheet
	m.paletteOrigin.sheet = m.reconcileLifecycleSheet(originalSheet)
	if m.paletteOrigin.project != nil {
		m.paletteOrigin.project = m.findLifecycleProject(m.paletteOrigin.project.WorkspaceRoot, m.paletteOrigin.project.ID)
	}
	if m.paletteOrigin.project == nil && (m.paletteOrigin.mode == viewEditProject || m.paletteOrigin.mode == viewNewWorktree) {
		m.paletteOrigin.mode = viewList
		m.paletteOrigin.commands = m.homePaletteCommands()
		m.paletteOrigin.title = "Commands · Home"
		m.statusMsg = "target is no longer available"
	}
	if originalSheet != m.paletteOrigin.sheet && m.paletteOrigin.sheet != nil {
		currentSheet := m.sheet
		m.sheet = m.paletteOrigin.sheet
		m.paletteOrigin.commands = m.sheetPaletteCommands()
		m.paletteOrigin.title = m.paletteTitle()
		m.sheet = currentSheet
		m.statusMsg = "target is no longer available"
	}
	if originalSheet != nil && m.paletteOrigin.sheet == nil {
		m.paletteOrigin.mode = viewList
		m.paletteOrigin.commands = m.homePaletteCommands()
		m.paletteOrigin.title = "Commands · Home"
		m.statusMsg = "target is no longer available"
	}
}
