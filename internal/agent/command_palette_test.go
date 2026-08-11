package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func paletteActions(commands []paletteCommand) map[string]paletteCommand {
	actions := make(map[string]paletteCommand, len(commands))
	for _, command := range commands {
		actions[command.action] = command
	}
	return actions
}

func requirePaletteActions(t *testing.T, commands []paletteCommand, expected ...string) {
	t.Helper()
	actions := paletteActions(commands)
	for _, action := range expected {
		if _, ok := actions[action]; !ok {
			t.Fatalf("missing %q in %#v", action, commands)
		}
	}
}

func TestCommandPaletteHomeContextsAndStandaloneRemoval(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha", Group: "team", Favorite: true}
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: []string{"team"}, Projects: []Project{p}}})
	m.items = []listItem{{kind: KindProject, project: &m.workspaces[0].Projects[0], workspaceRoot: "/ws"}}
	commands := m.paletteCommands()
	requirePaletteActions(t, commands, "open-project", "project-shell", "add-worktree", "edit-project", "favorite-project", "maintain-project", "search-local", "search-global", "switch-projection", "reverse-recent", "activity", "maintain-global", "quick-project")
	for _, command := range commands {
		if strings.HasPrefix(command.action, "standalone:") || command.group == "WORKSPACE" {
			t.Fatalf("standalone placeholder remains: %#v", command)
		}
	}

	m.items = []listItem{{kind: KindGroup, workspaceRoot: "/ws", group: "team"}}
	commands = m.paletteCommands()
	requirePaletteActions(t, commands, "open-group", "group-shell", "favorite-group", "maintain-group", "search-local", "activity")
	if _, ok := paletteActions(commands)["maintain-project"]; ok {
		t.Fatal("group inherited project action")
	}

	m.items = []listItem{{kind: KindGroup, group: "Go", projectionGroup: true}}
	commands = m.paletteCommands()
	if _, ok := paletteActions(commands)["open-group"]; ok {
		t.Fatal("projection heading received canonical group action")
	}
}

func TestCommandPaletteProjectAndGroupSheetSelections(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha", Group: "team"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: []string{"team"}, Projects: []Project{p}}})
	m.wtCache.SeedDetails(p.Path, []Worktree{{Path: p.Path, IsMain: true}, {Path: "/ws/alpha-feature", Branch: "feat/x"}})
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	m.sheet.focusWorktreePath(p.Path)
	commands := m.paletteCommands()
	requirePaletteActions(t, commands, "worktree-shell", "add-worktree", "search-sheet", "close-sheet", "activity")
	if _, ok := paletteActions(commands)["archive-worktrees"]; ok {
		t.Fatal("main worktree received destructive action")
	}
	m.sheet.focusWorktreePath("/ws/alpha-feature")
	commands = m.paletteCommands()
	requirePaletteActions(t, commands, "worktree-shell", "archive-worktrees", "delete-worktrees", "maintain-project")

	m.sheet.visual, m.sheet.visualAnchor = true, m.sheet.cursor
	commands = m.paletteCommands()
	actions := paletteActions(commands)
	requirePaletteActions(t, commands, "archive-worktrees", "delete-worktrees")
	if actions["archive-worktrees"].group != "SELECTED RANGE" || actions["delete-worktrees"].group != "SELECTED RANGE" {
		t.Fatalf("range sections = %#v", commands)
	}
	if _, ok := actions["worktree-shell"]; ok {
		t.Fatal("visual range retained cursor-specific action")
	}

	m.sheet = newGroupSheet(m, "/ws", "team")
	commands = m.paletteCommands()
	requirePaletteActions(t, commands, "open-project", "project-shell", "add-worktree", "group-shell", "favorite-group", "maintain-group", "search-sheet", "close-sheet", "activity")
}

func TestCommandPaletteSearchFormsLifecycleAndActivityContexts(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{p}}})
	m.items = []listItem{{kind: KindProject, project: &m.workspaces[0].Projects[0], workspaceRoot: "/ws"}}
	m.mode, m.flashEditing = viewFlash, false
	m.flashQuery.SetValue("alp")
	requirePaletteActions(t, m.paletteCommands(), "open-project", "resume-search", "clear-search", "cancel-search")

	m.mode, m.popupProj, m.wtField = viewNewWorktree, &m.workspaces[0].Projects[0], 1
	m.wtBranch.SetValue("feat/draft")
	commands := m.paletteCommands()
	if len(commands) != 2 {
		t.Fatalf("new worktree commands = %#v", commands)
	}
	m.openPalette()
	m.closePalette()
	if m.mode != viewNewWorktree || m.wtBranch.Value() != "feat/draft" || m.wtField != 1 {
		t.Fatalf("new worktree origin changed: mode=%v draft=%q field=%d", m.mode, m.wtBranch.Value(), m.wtField)
	}

	m.mode, m.editField = viewEditProject, 2
	m.editGroup.SetValue("draft-group")
	commands = m.paletteCommands()
	if len(commands) != 2 || commands[0].action != "save-project" {
		t.Fatalf("edit commands = %#v", commands)
	}
	m.openPalette()
	m.closePalette()
	if m.mode != viewEditProject || m.editGroup.Value() != "draft-group" || m.editField != 2 {
		t.Fatal("edit form origin changed")
	}

	for phase, expected := range map[lifecyclePhase][]string{
		lifecycleSelect:    {"lifecycle-projects", "lifecycle-worktrees", "close-lifecycle"},
		lifecycleThreshold: {"lifecycle-plan", "close-lifecycle"},
		lifecycleReview:    {"lifecycle-confirm", "close-lifecycle"},
		lifecycleRunning:   {"close-lifecycle"},
		lifecycleResult:    {"close-lifecycle", "activity-from-lifecycle"},
	} {
		m.mode, m.lifecycle = viewLifecycle, &lifecycleModel{phase: phase, scope: lifecycleScope{kind: lifecycleProject, project: &m.workspaces[0].Projects[0]}}
		requirePaletteActions(t, m.paletteCommands(), expected...)
	}

	m.mode = viewJobs
	m.lifecycle = nil
	m.jobs = []*explorerJob{{ID: "J0042", Label: "archive"}}
	m.jobsSelectedID = "J0042"
	commands = m.paletteCommands()
	requirePaletteActions(t, commands, "activity-detail", "activity-search", "activity-return")
	m.openPalette()
	if !strings.Contains(m.paletteOrigin.title, "Activity J0042") {
		t.Fatalf("activity title = %q", m.paletteOrigin.title)
	}
	m.closePalette()
	m.jobsDetail, m.jobsDetailScroll = true, 4
	commands = m.paletteCommands()
	requirePaletteActions(t, commands, "activity-feed", "activity-return")
	if _, ok := paletteActions(commands)["activity-search"]; ok {
		t.Fatal("Activity detail exposed a hidden search action")
	}
	if _, ok := paletteActions(commands)["retry"]; ok {
		t.Fatal("activity exposed retry")
	}
}

func TestCommandPaletteTitleSectionsInvocationAndDirectFiltering(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha", Favorite: true}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{p}}})
	m.items = []listItem{{kind: KindProject, project: &m.workspaces[0].Projects[0], workspaceRoot: "/ws"}}
	m.width, m.height = 100, 30
	m.openPalette()
	view := m.viewWhichKey()
	for _, text := range []string{"Commands · alpha", "SELECTED PROJECT", "HOME"} {
		if !strings.Contains(view, text) {
			t.Fatalf("palette missing %q: %q", text, view)
		}
	}
	groups := map[string]bool{}
	for _, command := range m.paletteOrigin.commands {
		groups[command.group] = true
	}
	for _, group := range []string{"SELECTED PROJECT", "HOME", "SESSION", "QUICK ACCESS"} {
		if !groups[group] {
			t.Fatalf("palette missing section %q: %#v", group, m.paletteOrigin.commands)
		}
	}
	m.paletteQuery.SetValue("organ")
	commands := m.filteredPaletteCommands()
	if len(commands) != 1 || commands[0].action != "edit-project" {
		t.Fatalf("filtered commands = %#v", commands)
	}
	m.updateWhichKey(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'w'}})
	if m.paletteQuery.Value() != "organw" || m.mode != viewWhichKey {
		t.Fatal("filtered direct key invoked command")
	}
	m.closePalette()
	m.openPalette()
	m.updateWhichKey(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'w'}})
	if m.mode != viewNewWorktree {
		t.Fatalf("empty-query direct key mode=%v", m.mode)
	}
}

func TestCommandPaletteOriginRestorationAndStableTargets(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	m := NewModel([]WorkspaceData{{Root: root, Projects: []Project{*project}}})
	m.wtCache.SeedDetails(project.Path, []Worktree{{Path: project.Path, IsMain: true}, *worktree})
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	m.sheet.focusWorktreePath(worktree.Path)
	m.sheet.filter.SetValue("feat")
	m.sheet.applyFilter()
	m.sheet.visual, m.sheet.visualAnchor = true, m.sheet.cursor
	origin := m.sheet
	m.openPalette()
	m.closePalette()
	if m.sheet != origin || !m.sheet.visual || m.sheet.filter.Value() != "feat" {
		t.Fatal("sheet origin was not restored exactly")
	}

	m.openPalette()
	deleteCommand := paletteActions(m.filteredPaletteCommands())["delete-worktrees"]
	m.paletteOrigin.sheet.focusWorktreePath(project.Path)
	m.invokePalette(deleteCommand)
	if m.lifecycle == nil || len(m.lifecycle.scope.worktrees) != 1 || m.lifecycle.scope.worktrees[0].Path != worktree.Path {
		t.Fatalf("destructive target drifted: %#v", m.lifecycle)
	}

	m.closeLifecycle()
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	m.sheet.focusWorktreePath(worktree.Path)
	m.openPalette()
	deleteCommand = paletteActions(m.filteredPaletteCommands())["delete-worktrees"]
	deleteCommand.worktrees[0].HEAD = "stale"
	m.invokePalette(deleteCommand)
	if m.lifecycle != nil || !strings.Contains(m.statusMsg, "HEAD changed") {
		t.Fatalf("stale target accepted: lifecycle=%#v status=%q", m.lifecycle, m.statusMsg)
	}
}

func TestCommandPaletteProjectionOrderCommandOnlyForRecent(t *testing.T) {
	m := NewModel(nil)
	m.homeView = config.ExplorerViewRecent
	if _, ok := paletteActions(m.paletteCommands())["reverse-recent"]; !ok {
		t.Fatal("Recent order command missing")
	}
	m.homeView = config.ExplorerViewProjects
	if _, ok := paletteActions(m.paletteCommands())["reverse-recent"]; ok {
		t.Fatal("Recent order command shown outside Recent")
	}
}

func TestCommandPaletteCloseKeysNeverInvokeOriginCommands(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{p}}})
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	origin := m.sheet
	m.openPalette()
	m.updateWhichKey(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'q'}})
	if m.mode != viewList || m.sheet != origin {
		t.Fatal("q invoked the sheet return command instead of closing the palette")
	}

	m.mode, m.popupProj = viewNewWorktree, &m.workspaces[0].Projects[0]
	m.wtBranch.SetValue("feat/draft")
	m.openPalette()
	m.updateWhichKey(tui.KeyMsg{Type: tui.KeyEsc})
	if m.mode != viewNewWorktree || m.wtBranch.Value() != "feat/draft" {
		t.Fatal("Esc canceled the form instead of closing the palette")
	}
}

func TestCommandPaletteNavigationNormalizesSearchAndPreservesGroupParent(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha", Group: "team"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: []string{"team"}, Projects: []Project{p}}})
	m.items = []listItem{{kind: KindProject, project: &m.workspaces[0].Projects[0], workspaceRoot: "/ws"}}
	m.mode = viewFlash
	m.flashQuery.SetValue("alp")
	m.flashMatches = []int{0}
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["open-project"])
	if m.mode != viewList || m.sheet == nil || m.sheet.target.ID != "alpha" {
		t.Fatalf("search project navigation left the wrong foreground: mode=%v sheet=%#v", m.mode, m.sheet)
	}

	group := newGroupSheet(m, "/ws", "team")
	m.sheet = group
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["open-project"])
	if m.sheet == nil || m.sheet.parent != group {
		t.Fatal("project picker lost its group-sheet parent")
	}
}

func TestCommandPaletteRestoresSheetAfterFormAndLifecycle(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{p}}})
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	origin := m.sheet
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["add-worktree"])
	if m.mode != viewNewWorktree {
		t.Fatal("add worktree did not open the form")
	}
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["cancel-form"])
	if m.mode != viewList || m.sheet != origin {
		t.Fatal("form did not return to its captured sheet")
	}

	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["maintain-project"])
	if m.lifecycle == nil || m.lifecycle.parentSheet != origin {
		t.Fatal("lifecycle did not capture its parent sheet")
	}
	m.closeLifecycle()
	if m.sheet != origin {
		t.Fatal("lifecycle did not restore its parent sheet")
	}
}

func TestCommandPaletteRejectsRemovedGroupAndEmptyWorktreeSubmission(t *testing.T) {
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: []string{"team"}}})
	m.items = []listItem{{kind: KindGroup, workspaceRoot: "/ws", group: "team"}}
	m.openPalette()
	open := paletteActions(m.filteredPaletteCommands())["open-group"]
	m.workspaces[0].Groups = nil
	m.invokePalette(open)
	if m.sheet != nil || m.statusMsg != "target is no longer available" {
		t.Fatalf("removed group was accepted: sheet=%#v status=%q", m.sheet, m.statusMsg)
	}

	m.mode = viewNewWorktree
	m.wtBranch.SetValue("")
	if _, ok := paletteActions(m.paletteCommands())["create-worktree"]; ok {
		t.Fatal("empty worktree form exposed a no-op create command")
	}
}

func TestCommandPalettePreservesSearchAcrossRefreshAndActivityOwnsForeground(t *testing.T) {
	m := NewModel(nil)
	m.mode, m.flashEditing = viewFlash, true
	m.flashQuery.SetValue("alpha")
	m.openPalette()
	state := m.captureFlashRefresh()
	if !state.active {
		t.Fatal("palette-hidden search was not captured as active")
	}
	m.restoreFlashRefresh(state)
	if m.mode != viewWhichKey {
		t.Fatalf("refresh displaced palette foreground: mode=%v", m.mode)
	}
	m.closePalette()
	if m.mode != viewFlash || m.flashQuery.Value() != "alpha" {
		t.Fatal("refresh corrupted the hidden search origin")
	}

	m.mode = viewList
	m.sheet = &sheet{mode: sheetProject}
	m.openActivity(m.sheet)
	if m.sheet != nil || m.jobsReturnSheet == nil {
		t.Fatal("Activity retained a hidden actionable sheet")
	}
	m.Update(tui.KeyMsg{Type: tui.KeyCtrlS, Ctrl: true})
	if m.Launch != nil {
		t.Fatal("Activity Ctrl+S launched the hidden origin")
	}
}

func TestCommandPaletteFormReturnsToCancellableGlobalSearch(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{p}}})
	m.openGlobalSearch()
	m.flashQuery.SetValue("alpha")
	m.recomputeFlash()
	m.flashEditing = false
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["add-worktree"])
	if m.mode != viewNewWorktree {
		t.Fatal("search action did not open worktree form")
	}
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["cancel-form"])
	if m.mode != viewFlash || m.flashQuery.Value() != "alpha" {
		t.Fatalf("form did not restore global search: mode=%v query=%q", m.mode, m.flashQuery.Value())
	}
	m.exitFlash(false)
	if m.mode != viewList || len(m.items) == 0 || m.items[0].project == nil || m.items[0].project.ID != "alpha" {
		t.Fatalf("global search cancellation lost Home origin: mode=%v items=%#v", m.mode, m.items)
	}
}

func TestCommandPaletteSearchLifecycleRestoresSearch(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	m := NewModel([]WorkspaceData{{Root: root, Projects: []Project{*project}}})
	m.wtCache.SeedDetails(project.Path, []Worktree{{Path: project.Path, IsMain: true}, *worktree})
	m.openGlobalSearch()
	m.flashQuery.SetValue(worktree.Branch)
	m.recomputeFlash()
	m.flashEditing = false
	for _, index := range m.flashMatches {
		if m.items[index].kind == KindWorktree && m.items[index].path == worktree.Path {
			m.cursor = index
			break
		}
	}
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["archive-worktrees"])
	if m.lifecycle == nil || m.lifecycleReturnFlash == nil {
		t.Fatal("search lifecycle did not capture its Search origin")
	}
	m.closeLifecycle()
	if m.mode != viewFlash || m.flashQuery.Value() != worktree.Branch {
		t.Fatalf("lifecycle cancel did not restore Search: mode=%v query=%q", m.mode, m.flashQuery.Value())
	}
}

func TestCommandPaletteActivityDetailEndsSearchEditing(t *testing.T) {
	m := NewModel(nil)
	m.mode = viewJobs
	m.jobs = []*explorerJob{{ID: "J0001"}}
	m.jobsSelectedID = "J0001"
	m.activitySearch, m.activityEditing = true, true
	m.openPalette()
	m.invokePalette(paletteActions(m.filteredPaletteCommands())["activity-detail"])
	if !m.jobsDetail || m.activityEditing {
		t.Fatalf("Activity detail retained hidden search input: detail=%v editing=%v", m.jobsDetail, m.activityEditing)
	}
}

func TestCommandPaletteWorktreeShellIgnoresDirtyStateDrift(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	m := NewModel([]WorkspaceData{{Root: root, Projects: []Project{*project}}})
	m.wtCache.SeedDetails(project.Path, []Worktree{{Path: project.Path, IsMain: true}, *worktree})
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	m.sheet.focusWorktreePath(worktree.Path)
	m.openPalette()
	openShell := paletteActions(m.filteredPaletteCommands())["worktree-shell"]
	if err := os.WriteFile(filepath.Join(worktree.Path, "dirty-after-palette"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.invokePalette(openShell)
	if m.Launch == nil || m.Launch.Cwd != worktree.Path {
		t.Fatalf("dirty worktree shell was rejected: launch=%#v status=%q", m.Launch, m.statusMsg)
	}
}

func TestCommandPaletteRefreshReplacesRemovedProjectSheetCommands(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Path: "/ws/alpha", Group: "team"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: []string{"team"}, Projects: []Project{p}}})
	group := newGroupSheet(m, "/ws", "team")
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], group)
	m.openPalette()
	m.workspaces[0].Projects = nil
	m.sheet = m.reconcileLifecycleSheet(m.sheet)
	m.reconcilePaletteAfterRefresh()
	actions := paletteActions(m.paletteOrigin.commands)
	if _, ok := actions["search-sheet"]; !ok || m.paletteOrigin.sheet != group {
		t.Fatalf("palette did not reconcile to parent group: origin=%#v actions=%#v", m.paletteOrigin.sheet, actions)
	}
	for _, command := range m.paletteOrigin.commands {
		if command.name == "Search worktrees" {
			t.Fatal("removed project retained project-sheet commands")
		}
	}
}
