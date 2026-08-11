package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/tui"
)

type paletteCommand struct {
	group, name, aliases, key, action string
	project                           ProjectIdentity
	groupRoot, groupName              string
	worktrees                         []Worktree
	jobID                             string
}

type paletteOrigin struct {
	mode     viewMode
	sheet    *sheet
	project  *Project
	title    string
	commands []paletteCommand
}

type whichKeyAction struct{ key, desc string }

func (m *Model) whichKeyActions() []whichKeyAction {
	commands := m.paletteCommands()
	actions := make([]whichKeyAction, len(commands))
	for i, command := range commands {
		actions[i] = whichKeyAction{command.key, command.name}
	}
	return actions
}

func command(group, name, aliases, key, action string) paletteCommand {
	return paletteCommand{group: group, name: name, aliases: aliases, key: key, action: action}
}

func projectCommand(group, name, aliases, key, action string, project *Project) paletteCommand {
	c := command(group, name, aliases, key, action)
	if project != nil {
		c.project = ProjectIdentity{WorkspaceRoot: project.WorkspaceRoot, ProjectID: project.ID}
	}
	return c
}

func groupCommand(group, name, aliases, key, action, root, groupName string) paletteCommand {
	c := command(group, name, aliases, key, action)
	c.groupRoot, c.groupName = root, groupName
	return c
}

func (m *Model) projectCommands(section string, project *Project, includePicker bool) []paletteCommand {
	if project == nil {
		return nil
	}
	commands := make([]paletteCommand, 0, 6)
	if includePicker {
		commands = append(commands, projectCommand(section, "Choose worktree", "open picker", "enter", "open-project", project))
	}
	commands = append(commands,
		projectCommand(section, "Open main shell", "shell", "s", "project-shell", project),
		projectCommand(section, "Add worktree", "new branch", "w", "add-worktree", project),
		projectCommand(section, "Edit organization", "group category", "e", "edit-project", project),
		projectCommand(section, m.paletteFavoriteLabel(project), "star", "f", "favorite-project", project),
		projectCommand(section, "Project maintenance", "archive cleanup", "M", "maintain-project", project),
	)
	return commands
}

func (m *Model) groupCommands(section, root, name string, includeOpen bool) []paletteCommand {
	commands := make([]paletteCommand, 0, 4)
	shellKey, favoriteKey, maintenanceKey := "g", "F", ""
	if includeOpen {
		commands = append(commands, groupCommand(section, "Open group", "projects picker", "enter", "open-group", root, name))
		shellKey, favoriteKey, maintenanceKey = "s", "f", "M"
	}
	return append(commands,
		groupCommand(section, "Open group shell", "shell", shellKey, "group-shell", root, name),
		groupCommand(section, "Toggle group favorite", "star", favoriteKey, "favorite-group", root, name),
		groupCommand(section, "Group maintenance", "archive cleanup", maintenanceKey, "maintain-group", root, name),
	)
}

func (m *Model) homeViewCommands() []paletteCommand {
	commands := []paletteCommand{
		command("HOME", "Search current view", "find local", "/", "search-local"),
		command("HOME", "Search all", "global find", "S", "search-global"),
		command("HOME", "Switch projection", "recent projects language", "v", "switch-projection"),
	}
	if m.homeView == config.ExplorerViewRecent {
		commands = append(commands, command("HOME", "Reverse Recent order", "ascending descending", "o", "reverse-recent"))
	}
	return commands
}

func (m *Model) homeSessionCommands() []paletteCommand {
	return []paletteCommand{
		command("SESSION", "Open Activity", "actions history", "A", "activity"),
		command("SESSION", "Global maintenance", "archive cleanup", "", "maintain-global"),
	}
}

func (m *Model) quickCommands() []paletteCommand {
	commands := make([]paletteCommand, 0, len(m.headerChips))
	for i, slot := range m.headerChips {
		c := command("QUICK ACCESS", fmt.Sprintf("%d → %s", i+1, presentLabel(slot.Name)), "favorite recent", fmt.Sprint(i+1), "quick-project")
		c.project = ProjectIdentity{WorkspaceRoot: slot.WorkspaceRoot, ProjectID: slot.Project.ID}
		commands = append(commands, c)
	}
	return commands
}

func (m *Model) paletteCommands() []paletteCommand {
	if m.mode == viewNewWorktree {
		commands := []paletteCommand{command("NEW WORKTREE", "Cancel", "return", "", "cancel-form")}
		if strings.TrimSpace(m.wtBranch.Value()) != "" {
			commands = append([]paletteCommand{command("NEW WORKTREE", "Create worktree", "save submit", "enter", "create-worktree")}, commands...)
		}
		return commands
	}
	if m.mode == viewEditProject {
		return []paletteCommand{command("EDIT PROJECT", "Save changes", "submit", "enter", "save-project"), command("EDIT PROJECT", "Cancel", "return", "", "cancel-form")}
	}
	if m.mode == viewLifecycle {
		return m.lifecyclePaletteCommands()
	}
	if m.mode == viewJobs {
		return m.activityPaletteCommands()
	}
	if m.mode == viewFlash {
		return m.searchPaletteCommands()
	}
	if m.sheet != nil {
		return m.sheetPaletteCommands()
	}
	return m.homePaletteCommands()
}

func (m *Model) homePaletteCommands() []paletteCommand {
	var commands []paletteCommand
	if item := m.currentItem(); item != nil {
		switch {
		case item.kind == KindProject:
			commands = append(commands, m.projectCommands("SELECTED PROJECT", item.project, true)...)
		case item.kind == KindGroup && !item.projectionGroup:
			commands = append(commands, m.groupCommands("SELECTED GROUP", item.workspaceRoot, item.group, true)...)
		}
	}
	commands = append(commands, m.homeViewCommands()...)
	commands = append(commands, m.homeSessionCommands()...)
	commands = append(commands, m.quickCommands()...)
	return commands
}

func (m *Model) sheetPaletteCommands() []paletteCommand {
	s := m.sheet
	var commands []paletteCommand
	if s.mode == sheetProject {
		if selected := s.visualWorktrees(); len(selected) > 0 {
			commands = append(commands, m.worktreeCommand("SELECTED RANGE", "Archive selected checkouts", "preserve branches", "a", "archive-worktrees", s.target, selected), m.worktreeCommand("SELECTED RANGE", "Delete selected checkouts and branches", "remove branches", "d", "delete-worktrees", s.target, selected))
		} else if row := s.focused(); row != nil && row.wt != nil {
			commands = append(commands, m.worktreeCommand("SELECTED WORKTREE", "Open shell", "launch", "enter", "worktree-shell", s.target, []*Worktree{row.wt}))
			if !row.wt.IsMain {
				commands = append(commands, m.worktreeCommand("SELECTED WORKTREE", "Archive checkout", "preserve branch", "a", "archive-worktrees", s.target, []*Worktree{row.wt}), m.worktreeCommand("SELECTED WORKTREE", "Delete checkout and branches", "remove branch", "d", "delete-worktrees", s.target, []*Worktree{row.wt}))
			}
		}
		commands = append(commands, m.projectCommands("PARENT PROJECT", s.target, false)...)
		commands = append(commands, command("PROJECT VIEW", "Search worktrees", "filter", "/", "search-sheet"), command("PROJECT VIEW", "Return Home", "back", "", "close-sheet"), command("SESSION", "Open Activity", "actions history", "A", "activity"))
		return commands
	}
	if row := s.focused(); row != nil && row.proj != nil {
		commands = append(commands, m.projectCommands("SELECTED PROJECT", row.proj, true)...)
	}
	commands = append(commands, m.groupCommands("PARENT GROUP", s.workspaceRoot, s.group, false)...)
	commands = append(commands, command("GROUP VIEW", "Search projects", "filter", "/", "search-sheet"), command("GROUP VIEW", "Return Home", "back", "", "close-sheet"), command("SESSION", "Open Activity", "actions history", "A", "activity"))
	return commands
}

func (m *Model) worktreeCommand(section, name, aliases, key, action string, project *Project, worktrees []*Worktree) paletteCommand {
	c := projectCommand(section, name, aliases, key, action, project)
	for _, worktree := range worktrees {
		c.worktrees = append(c.worktrees, *worktree)
	}
	return c
}

func (m *Model) searchPaletteCommands() []paletteCommand {
	var commands []paletteCommand
	if item := m.currentItem(); item != nil {
		switch item.kind {
		case KindProject:
			commands = append(commands, m.projectCommands("SELECTED RESULT", item.project, true)...)
		case KindWorktree:
			if item.worktree != nil {
				commands = append(commands, m.worktreeCommand("SELECTED RESULT", "Open shell", "launch", "enter", "worktree-shell", item.parentProj, []*Worktree{item.worktree}))
				if !item.worktree.IsMain {
					commands = append(commands, m.worktreeCommand("SELECTED RESULT", "Archive checkout", "preserve branch", "a", "archive-worktrees", item.parentProj, []*Worktree{item.worktree}), m.worktreeCommand("SELECTED RESULT", "Delete checkout and branches", "remove branch", "d", "delete-worktrees", item.parentProj, []*Worktree{item.worktree}))
				}
				commands = append(commands, m.projectCommands("PARENT PROJECT", item.parentProj, false)...)
			}
		case KindGroup:
			if !item.projectionGroup {
				commands = append(commands, m.groupCommands("SELECTED RESULT", item.workspaceRoot, item.group, true)...)
			}
		}
	}
	return append(commands, command("SEARCH", "Resume query editing", "type filter", "/", "resume-search"), command("SEARCH", "Clear query", "reset", "", "clear-search"), command("SEARCH", "Cancel search", "restore origin", "", "cancel-search"))
}

func (m *Model) lifecyclePaletteCommands() []paletteCommand {
	lm := m.lifecycle
	if lm == nil {
		return nil
	}
	switch lm.phase {
	case lifecycleSelect:
		return []paletteCommand{command("TASK", "Choose archive projects", "projects", "1", "lifecycle-projects"), command("TASK", "Choose archive old worktrees", "worktrees", "2", "lifecycle-worktrees"), command("TASK", "Cancel", "return", "", "close-lifecycle")}
	case lifecycleThreshold:
		return []paletteCommand{command("TASK", "Plan archive", "review threshold", "enter", "lifecycle-plan"), command("TASK", "Cancel", "return", "", "close-lifecycle")}
	case lifecycleReview:
		return []paletteCommand{command("TASK", "Confirm operation", "execute", "enter", "lifecycle-confirm"), command("TASK", "Cancel", "return", "", "close-lifecycle")}
	case lifecycleResult:
		return []paletteCommand{command("TASK", "Return", "close", "", "close-lifecycle"), command("TASK", "Open Activity", "history", "A", "activity-from-lifecycle")}
	default:
		return []paletteCommand{command("TASK", "Return while task continues", "close", "", "close-lifecycle")}
	}
}

func (m *Model) activityPaletteCommands() []paletteCommand {
	if m.jobsDetail {
		return []paletteCommand{command("ACTIVITY", "Return to feed", "back", "", "activity-feed"), command("ACTIVITY", "Return to captured origin", "close", "", "activity-return")}
	}
	commands := []paletteCommand{}
	if job := m.findJob(m.jobsSelectedID); job != nil {
		c := command("SELECTED JOB", "Open details", "inspect", "enter", "activity-detail")
		c.jobID = job.ID
		commands = append(commands, c)
	}
	return append(commands, command("ACTIVITY", "Search Activity", "filter jobs", "/", "activity-search"), command("ACTIVITY", "Return to captured origin", "close", "", "activity-return"))
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
	case viewLifecycle:
		return "Commands · " + m.lifecycleScopeLabel() + " · " + m.lifecyclePhaseLabel()
	case viewJobs:
		if m.jobsSelectedID != "" {
			return "Commands · Activity " + m.jobsSelectedID
		}
		return "Commands · Activity"
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

func (m *Model) filteredPaletteCommands() []paletteCommand {
	query := strings.ToLower(strings.TrimSpace(m.paletteQuery.Value()))
	commands := m.paletteCommands()
	if m.paletteOrigin != nil && m.mode == viewWhichKey {
		commands = m.paletteOrigin.commands
	}
	filtered := make([]paletteCommand, 0, len(commands))
	for _, command := range commands {
		if query == "" || strings.Contains(strings.ToLower(command.name+" "+command.aliases), query) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

func (m *Model) openPalette() tui.Cmd {
	origin := &paletteOrigin{mode: m.mode, sheet: m.sheet, project: m.popupProj, title: m.paletteTitle()}
	origin.commands = m.paletteCommands()
	m.paletteOrigin = origin
	m.mode = viewWhichKey
	m.paletteCursor = 0
	m.paletteQuery.SetValue("")
	return m.paletteQuery.Focus()
}

func (m *Model) closePalette() {
	m.paletteQuery.Blur()
	if m.paletteOrigin != nil {
		m.mode, m.sheet, m.popupProj = m.paletteOrigin.mode, m.paletteOrigin.sheet, m.paletteOrigin.project
		m.paletteOrigin = nil
		return
	}
	m.mode = viewList
}

func (m *Model) updateWhichKey(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	commands := m.filteredPaletteCommands()
	queryEmpty := strings.TrimSpace(m.paletteQuery.Value()) == ""
	switch msg.String() {
	case "ctrl+o", "ctrl+c", "q", "esc":
		m.closePalette()
		return m, nil
	case "j", "down":
		m.paletteCursor = min(max(0, len(commands)-1), m.paletteCursor+1)
		return m, nil
	case "k", "up":
		m.paletteCursor = max(0, m.paletteCursor-1)
		return m, nil
	case "enter":
		if m.paletteCursor < len(commands) {
			return m.invokePalette(commands[m.paletteCursor])
		}
		return m, nil
	default:
		if queryEmpty {
			for _, command := range commands {
				if command.key == msg.String() {
					return m.invokePalette(command)
				}
			}
		}
		var cmd tui.Cmd
		m.paletteQuery, cmd = m.paletteQuery.Update(msg)
		m.paletteCursor = 0
		return m, cmd
	}
}

func (m *Model) invokePalette(command paletteCommand) (tui.Model, tui.Cmd) {
	origin := m.paletteOrigin
	m.closePalette()
	project := m.findLifecycleProject(command.project.WorkspaceRoot, command.project.ProjectID)
	if command.project.ProjectID != "" && project == nil {
		m.statusMsg = "target is no longer available"
		return m, nil
	}
	switch command.action {
	case "open-project", "quick-project":
		if origin != nil && origin.mode == viewFlash {
			m.exitFlash(true)
		}
		var parent *sheet
		if origin != nil && origin.sheet != nil && origin.sheet.mode == sheetGroup {
			parent = origin.sheet
		}
		m.mode = viewList
		m.sheet = newProjectSheet(m, project, parent)
	case "project-shell":
		return m.launch(project.WorkspaceRoot, project.Path)
	case "worktree-shell":
		worktree, err := m.resolvePaletteWorktreePath(project, command.worktrees)
		if err != nil {
			m.statusMsg = err.Error()
			return m, nil
		}
		return m.launch(project.WorkspaceRoot, worktree.Path)
	case "add-worktree", "edit-project", "favorite-project", "maintain-project":
		if command.action == "add-worktree" || command.action == "edit-project" {
			m.captureFormOrigin(origin)
		}
		if command.action == "maintain-project" {
			m.captureLifecycleOrigin(origin)
		}
		return m.invokeProjectPaletteAction(project, command.action, origin)
	case "open-group":
		if !m.paletteGroupExists(command.groupRoot, command.groupName) {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		if origin != nil && origin.mode == viewFlash {
			m.exitFlash(true)
		}
		m.mode = viewList
		m.sheet = newGroupSheet(m, command.groupRoot, command.groupName)
	case "group-shell":
		if !m.paletteGroupExists(command.groupRoot, command.groupName) {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		return m.launch(command.groupRoot, groupRootPath(command.groupRoot, command.groupName))
	case "favorite-group":
		if !m.paletteGroupExists(command.groupRoot, command.groupName) {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		return m, m.toggleFavoriteGroup(command.groupRoot, command.groupName)
	case "maintain-group":
		if !m.paletteGroupExists(command.groupRoot, command.groupName) {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		m.captureLifecycleOrigin(origin)
		m.sheet = nil
		m.openLifecycle(lifecycleScope{kind: lifecycleGroup, workspaceRoot: command.groupRoot, group: command.groupName})
	case "archive-worktrees", "delete-worktrees":
		worktrees, err := m.resolvePaletteWorktrees(project, command.worktrees)
		if err != nil {
			m.statusMsg = err.Error()
			return m, nil
		}
		m.captureLifecycleOrigin(origin)
		m.sheet = nil
		if command.action == "archive-worktrees" {
			m.openWorktreeArchiveMany(project, worktrees)
		} else {
			m.openWorktreeDeleteMany(project, worktrees)
		}
		if origin != nil {
			m.lifecycle.parentSheet = origin.sheet
		}
	case "search-local":
		m.openLocalSearch()
	case "search-global":
		m.openGlobalSearch()
	case "switch-projection":
		m.switchHomeProjection()
	case "reverse-recent":
		m.reverseRecentOrder()
	case "activity":
		m.openActivity(origin.sheet)
	case "maintain-global":
		m.captureLifecycleOrigin(origin)
		m.openLifecycle(lifecycleScope{kind: lifecycleGlobal})
	case "search-sheet":
		if m.sheet == nil {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		m.sheet.captureSearchOrigin()
		m.sheet.filterMode = true
		return m, m.sheet.filter.Focus()
	case "close-sheet":
		if m.sheet == nil {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		return m.sheet.close(m)
	case "resume-search":
		m.flashEditing = true
		return m, m.flashQuery.Focus()
	case "clear-search":
		m.flashQuery.SetValue("")
		m.recomputeFlash()
	case "cancel-search":
		m.exitFlash(false)
	case "create-worktree":
		return m.executeNewWorktree()
	case "save-project":
		return m.executeEditProject()
	case "cancel-form":
		if m.mode == viewNewWorktree {
			m.wtBranch.Blur()
		} else {
			m.editGroup.Blur()
			m.editErr = ""
		}
		m.restoreFormOrigin()
	case "lifecycle-projects":
		m.updateLifecycleSelect("1")
	case "lifecycle-worktrees":
		m.updateLifecycleSelect("2")
	case "lifecycle-plan":
		return m, m.updateLifecycleThreshold(tui.KeyMsg{Type: tui.KeyEnter})
	case "lifecycle-confirm":
		return m, m.startLifecycleJob()
	case "close-lifecycle":
		m.closeLifecycle()
	case "activity-from-lifecycle":
		returnSheet := m.lifecycle.parentSheet
		returnFlash := m.lifecycleReturnFlash
		m.lifecycleReturnFlash = nil
		m.closeLifecycle()
		m.openActivity(returnSheet)
		m.activityReturnFlash = returnFlash
	case "activity-detail":
		if m.findJob(command.jobID) == nil {
			m.statusMsg = "target is no longer available"
			return m, nil
		}
		m.activityEditing = false
		m.activityQuery.Blur()
		m.jobsSelectedID, m.jobsDetail, m.jobsDetailScroll = command.jobID, true, 0
	case "activity-feed":
		m.jobsDetail, m.jobsDetailScroll = false, 0
	case "activity-search":
		m.activityOriginID = m.jobsSelectedID
		m.activitySearch, m.activityEditing = true, true
		m.activityQuery.SetValue("")
		return m, m.activityQuery.Focus()
	case "activity-return":
		m.closeActivity()
	}
	return m, nil
}

func (m *Model) resolvePaletteWorktrees(project *Project, reviewed []Worktree) ([]*Worktree, error) {
	if project == nil || len(reviewed) == 0 {
		return nil, fmt.Errorf("target is no longer available")
	}
	live, err := LoadWorktrees(project.Path)
	if err != nil {
		return nil, fmt.Errorf("target is no longer available: %w", err)
	}
	resolved := make([]*Worktree, 0, len(reviewed))
	for i := range reviewed {
		found := false
		for j := range live {
			if live[j].Path != reviewed[i].Path {
				continue
			}
			if err := validateReviewedWorktree(&reviewed[i], &live[j]); err != nil {
				return nil, err
			}
			copy := live[j]
			resolved = append(resolved, &copy)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("target is no longer available")
		}
	}
	return resolved, nil
}

func (m *Model) resolvePaletteWorktreePath(project *Project, reviewed []Worktree) (*Worktree, error) {
	if project == nil || len(reviewed) != 1 {
		return nil, fmt.Errorf("target is no longer available")
	}
	live, err := LoadWorktrees(project.Path)
	if err != nil {
		return nil, fmt.Errorf("target is no longer available: %w", err)
	}
	for i := range live {
		if live[i].Path == reviewed[0].Path {
			return &live[i], nil
		}
	}
	return nil, fmt.Errorf("target is no longer available")
}

func (m *Model) invokeProjectPaletteAction(project *Project, action string, origin *paletteOrigin) (tui.Model, tui.Cmd) {
	switch action {
	case "add-worktree":
		m.popupProj = project
		m.wtBranch.SetValue("")
		m.wtBranch.Focus()
		m.wtField, m.sheet, m.mode = 0, nil, viewNewWorktree
	case "edit-project":
		m.popupProj = project
		m.editGroup.SetValue(project.Group)
		m.editGroup.Focus()
		m.editCategory = config.Category(project.Category)
		if m.editCategory == "" {
			m.editCategory = config.CategoryPersonal
		}
		m.editField, m.editErr, m.sheet, m.mode = 0, "", nil, viewEditProject
	case "favorite-project":
		return m, m.toggleFavoriteFor(project)
	case "maintain-project":
		m.sheet = nil
		m.openLifecycle(lifecycleScope{kind: lifecycleProject, project: project})
		if origin != nil {
			m.lifecycle.parentSheet = origin.sheet
		}
	}
	return m, nil
}

func (m *Model) captureFormOrigin(origin *paletteOrigin) {
	m.formReturnSheet = nil
	m.formReturnFlash = nil
	if origin == nil {
		return
	}
	m.formReturnSheet = origin.sheet
	if origin.mode == viewFlash {
		state := m.captureFlashRefresh()
		m.formReturnFlash = &state
		m.exitFlash(true)
	}
}

func (m *Model) restoreFormOrigin() {
	returnSheet, returnFlash := m.formReturnSheet, m.formReturnFlash
	m.formReturnSheet, m.formReturnFlash = nil, nil
	m.mode, m.sheet = viewList, returnSheet
	if returnFlash != nil {
		m.restoreFlashRefresh(*returnFlash)
	}
}

func (m *Model) captureLifecycleOrigin(origin *paletteOrigin) {
	m.lifecycleReturnFlash = nil
	if origin == nil || origin.mode != viewFlash {
		return
	}
	state := m.captureFlashRefresh()
	m.lifecycleReturnFlash = &state
	m.exitFlash(true)
}

func (m *Model) paletteGroupExists(root, group string) bool {
	for _, workspace := range m.workspaces {
		if workspace.Root != root {
			continue
		}
		for _, candidate := range workspace.Groups {
			if candidate == group {
				return true
			}
		}
		return false
	}
	return false
}

func (m *Model) openLocalSearch() {
	m.savedCursor, m.savedScroll = m.cursor, m.scroll
	m.flashGlobal, m.flashEditing, m.mode = false, true, viewFlash
	m.flashQuery.SetValue("")
	m.flashQuery.Focus()
	m.recomputeFlash()
}

func (m *Model) switchHomeProjection() {
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
}

func (m *Model) reverseRecentOrder() {
	if m.homeView != config.ExplorerViewRecent {
		return
	}
	if m.recentOrder == config.RecentOrderDesc {
		m.recentOrder = config.RecentOrderAsc
	} else {
		m.recentOrder = config.RecentOrderDesc
	}
	m.saveExplorerPreferences()
	m.rebuildItems()
}

func (m *Model) paletteFavoriteLabel(project *Project) string {
	if project.Favorite {
		return "Remove favorite"
	}
	return "Add favorite"
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
	case viewLifecycle:
		return m.viewLifecycle()
	case viewJobs:
		return m.viewJobs()
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

func (m *Model) toggleFavoriteGroup(root, group string) tui.Cmd {
	if root == "" {
		m.statusMsg = "cannot resolve workspace for group"
		return nil
	}
	return m.submitJob("favorite @"+group, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		ctx.withRegistry(root, func() {
			ws, err := config.Load(root)
			if err != nil {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: err.Error()}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			current, ok := ws.Groups[group]
			if !ok {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: "group is not declared in workspace.toml"}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			current.Favorite = !current.Favorite
			ws.Groups[group] = current
			if err := config.Save(root, ws); err != nil {
				outcome = targetOutcome{Target: group, Kind: targetFailed, Detail: err.Error()}
			} else {
				outcome = targetOutcome{Target: group, Kind: targetSuccess, Detail: "saved"}
			}
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{WorkspaceRoot: root}}}, outcome.Kind == targetSuccess)
		})
		metrics.RecordExplorerFavoriteChanged()
		return jobResult{Summary: "favorite updated", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{WorkspaceRoot: root}}}
	})
}

func (m *Model) toggleFavoriteFor(proj *Project) tui.Cmd {
	root := m.workspaceRootFor(proj)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for project"
		return nil
	}
	projectID, name := proj.ID, proj.Name
	return m.submitJob("favorite "+name, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		ctx.withRegistry(root, func() {
			ws, err := config.Load(root)
			if err != nil {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: err.Error()}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			p, ok := ws.Projects[projectID]
			if !ok {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: "project is missing from workspace.toml"}
				ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
				return
			}
			p.SetFavorite(!p.Favorite)
			ws.Projects[projectID] = p
			if err := config.Save(root, ws); err != nil {
				outcome = targetOutcome{Target: name, Kind: targetFailed, Detail: err.Error()}
			} else {
				outcome = targetOutcome{Target: name, Kind: targetSuccess, Detail: "saved"}
			}
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{root, projectID}}}, outcome.Kind == targetSuccess)
		})
		metrics.RecordExplorerFavoriteChanged()
		return jobResult{Summary: "favorite updated", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{root, projectID}}}
	})
}

func outcomeError(outcome targetOutcome) string {
	if outcome.Kind == targetFailed || outcome.Kind == targetPartial {
		return outcome.Detail
	}
	return ""
}
