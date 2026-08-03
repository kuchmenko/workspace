package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
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

type Worktree struct {
	Path   string
	Branch string
	IsMain bool
	Dirty  bool
	Ahead  int
}

func explorerMachineName() (string, error) {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return "", fmt.Errorf("load machine config: %w", err)
	}
	if machine == nil || machine.MachineName == "" {
		return "", fmt.Errorf("machine name is not configured")
	}
	return machine.MachineName, nil
}

func worktreeDisplayName(wt Worktree) string {
	if wt.IsMain {
		return "main"
	}
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	if wt.Branch != "" {
		return wt.Branch
	}
	return filepath.Base(wt.Path)
}

type WorktreeCache struct {
	data map[string][]Worktree
}

func NewWorktreeCache() *WorktreeCache {
	return &WorktreeCache{data: make(map[string][]Worktree)}
}

func (c *WorktreeCache) Get(mainPath string) []Worktree {
	if wts, ok := c.data[mainPath]; ok {
		return wts
	}
	wts := LoadWorktrees(mainPath)
	c.data[mainPath] = wts
	return wts
}

func (c *WorktreeCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

func LoadWorktrees(mainPath string) []Worktree {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	var result []Worktree
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		w := Worktree{
			Path:   wt.Path,
			Branch: wt.Branch,
			IsMain: wt.Path == mainPath,
			Dirty:  git.IsDirty(wt.Path),
		}
		ahead, _, _ := git.AheadBehind(wt.Path, wt.Branch)
		w.Ahead = ahead
		result = append(result, w)
	}
	return result
}

func (m *Model) updateNewWorktree(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = viewList
		return m, nil
	case "tab", "down":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "shift+tab", "up":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "enter":
		if m.wtField == 1 {
			return m.executeNewWorktree()
		}
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "backspace":
		if m.wtField == 0 && len(m.wtBranch) > 0 {
			m.wtBranch = m.wtBranch[:len(m.wtBranch)-1]
		}
		return m, nil
	default:
		if m.wtField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.wtBranch += key
		}
	}
	return m, nil
}

func (m *Model) executeNewWorktree() (tui.Model, tui.Cmd) {
	branch := strings.TrimSpace(m.wtBranch)
	if branch == "" {
		return m, nil
	}

	wsRoot := m.workspaceRootFor(m.popupProj)
	machine, err := explorerMachineName()
	if err == nil {
		_, err = repo.AddWorktree(repo.WorktreeAddOptions{WorkspaceRoot: wsRoot, Project: m.popupProj.ID, Branch: branch, Machine: machine})
	}
	if err != nil {
		m.statusMsg = err.Error()
		m.mode = viewList
		return m, nil
	}
	m.wtCache.Invalidate(m.popupProj.Path)

	m.mode = viewList
	m.rebuildItems()
	m.ensureVisible()
	m.statusMsg = "worktree created"
	return m, nil
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

	branchLabel := "  Branch name:"
	branchVal := m.wtBranch + "█"
	if m.wtField != 0 {
		branchVal = m.wtBranch
		if branchVal == "" {
			branchVal = "(required)"
		}
	}
	if m.wtField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+branchVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+branchVal))
	}
	if branch := strings.TrimSpace(m.wtBranch); branch != "" {
		pathPreview := fmt.Sprintf("  → dir: %s-wt-<machine>-%s", p.Name, layout.SlugifyBranch(branch))
		lines = append(lines, popupDimStyle.Width(innerW).Render(pathPreview))
	}
	lines = append(lines, "")

	confirmLabel := "  → Create worktree"
	if m.wtField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(confirmLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(confirmLabel))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  enter:confirm  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}

func EditProjectMetadata(wsRoot, projID, group string, category config.Category) error {
	if wsRoot == "" {
		return fmt.Errorf("workspace root required")
	}
	if projID == "" {
		return fmt.Errorf("project id required")
	}
	if category != config.CategoryPersonal && category != config.CategoryWork {
		return fmt.Errorf("category must be %q or %q", config.CategoryPersonal, config.CategoryWork)
	}

	ws, err := config.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("load workspace.toml: %w", err)
	}
	proj, ok := ws.Projects[projID]
	if !ok {
		return fmt.Errorf("project %q not found in workspace.toml", projID)
	}
	proj.Group = strings.TrimSpace(group)
	proj.Category = category
	ws.Projects[projID] = proj

	if err := config.Save(wsRoot, ws); err != nil {
		return fmt.Errorf("save workspace.toml: %w", err)
	}
	return nil
}

func existingGroups(workspaces []WorkspaceData) []string {
	seen := map[string]bool{}
	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			if g != "" {
				seen[g] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func (m *Model) updateEditProject(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
		m.editErr = ""
		return m, nil
	case "tab", "down":
		m.editField = (m.editField + 1) % 3
		return m, nil
	case "shift+tab", "up":
		m.editField = (m.editField + 2) % 3
		return m, nil
	case "enter":
		if m.editField == 2 {
			return m.executeEditProject()
		}
		m.editField = (m.editField + 1) % 3
		return m, nil
	case " ":
		if m.editField == 1 {
			if m.editCategory == config.CategoryPersonal {
				m.editCategory = config.CategoryWork
			} else {
				m.editCategory = config.CategoryPersonal
			}
			return m, nil
		}
		if m.editField == 0 {
			m.editGroup += " "
			return m, nil
		}
	case "backspace":
		if m.editField == 0 && len(m.editGroup) > 0 {
			m.editGroup = m.editGroup[:len(m.editGroup)-1]
		}
		return m, nil
	default:
		if m.editField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.editGroup += key
		}
	}
	return m, nil
}

func (m *Model) executeEditProject() (tui.Model, tui.Cmd) {
	proj := m.popupProj
	if proj == nil {
		m.mode = viewList
		return m, nil
	}
	wsRoot := m.workspaceRootFor(proj)
	if wsRoot == "" {
		m.editErr = "workspace root not found"
		return m, nil
	}

	newGroup := strings.TrimSpace(m.editGroup)
	newCat := m.editCategory

	if err := EditProjectMetadata(wsRoot, proj.ID, newGroup, newCat); err != nil {
		m.editErr = err.Error()
		return m, nil
	}

	for wi := range m.workspaces {
		if m.workspaces[wi].Root != wsRoot {
			continue
		}
		ws := &m.workspaces[wi]
		for pi := range ws.Projects {
			if ws.Projects[pi].ID != proj.ID {
				continue
			}
			ws.Projects[pi].Group = newGroup
			ws.Projects[pi].Category = string(newCat)
			break
		}
		ws.Groups = recomputeGroups(ws.Projects)
		if newGroup != "" {
			m.expanded[newGroup] = true
		}
		break
	}

	m.editErr = ""
	m.mode = viewList
	m.statusMsg = fmt.Sprintf("updated %s: group=%s category=%s",
		proj.Name, displayGroup(newGroup), newCat)
	m.rebuildItems()

	m.jumpToProject(proj.ID)
	m.ensureVisible()
	return m, nil
}

func displayGroup(g string) string {
	if g == "" {
		return "(none)"
	}
	return g
}

func recomputeGroups(projects []Project) []string {
	seen := map[string]bool{}
	for _, p := range projects {
		if p.Group != "" {
			seen[p.Group] = true
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func (m *Model) viewEditProject() string {
	p := m.popupProj
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	title := "Edit project"
	if p != nil {
		title = fmt.Sprintf("Edit project: %s", p.Name)
	}
	lines = append(lines, popupTitleStyle.Width(innerW).Render(title))
	lines = append(lines, "")

	groupLabel := "  Group:"
	groupVal := m.editGroup
	if m.editField == 0 {
		groupVal += "█"
	} else if groupVal == "" {
		groupVal = "(none)"
	}
	if m.editField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(groupLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+groupVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(groupLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+groupVal))
	}
	if hint := groupHint(m.workspaces); hint != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render("  existing: "+hint))
	}
	lines = append(lines, "")

	catLabel := "  Category:"
	catVal := string(m.editCategory) + "   (space to toggle: personal | work)"
	if m.editField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(catLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+string(m.editCategory)))
		lines = append(lines, popupDimStyle.Width(innerW).Render("    space toggles personal | work"))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(catLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+catVal))
	}
	lines = append(lines, "")

	saveLabel := "  → Save"
	if m.editField == 2 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(saveLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(saveLabel))
	}

	if m.editErr != "" {
		lines = append(lines, "")
		lines = append(lines, popupTitleStyle.Width(innerW).Render("error: "+m.editErr))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  space:toggle  enter:save  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}

func groupHint(workspaces []WorkspaceData) string {
	groups := existingGroups(workspaces)
	if len(groups) == 0 {
		return ""
	}
	const max = 5
	if len(groups) > max {
		groups = append(groups[:max], "…")
	}
	return strings.Join(groups, " · ")
}

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
		return []whichKeyAction{
			{"n", "new worktree"},
			{"", ""},
			{"esc", "back"},
		}
	}

	switch item.kind {
	case KindGroup:
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"f", m.favoriteToggleLabelGroup(item.group)},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"f", m.favoriteToggleLabel(item)},
			{"w", "worktree ›"},
			{"e", "edit"},
			{"l", "shell"},
			{"", ""},
			{"esc", "close"},
		}
	}
	return nil
}

func (m *Model) favoriteToggleLabel(it *listItem) string {
	if it != nil && it.project != nil && it.project.Favorite {
		return "unfavorite"
	}
	return "favorite"
}

func (m *Model) favoriteToggleLabelGroup(group string) string {
	for _, ws := range m.workspaces {
		if ws.FavoriteGroups[group] {
			return "unfavorite"
		}
	}
	return "favorite"
}

func (m *Model) updateWhichKey(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	item := m.currentItem()

	if m.whichKeyLevel == 1 {
		switch key {
		case "esc":
			m.whichKeyLevel = 0
			return m, nil
		case "n":
			if item != nil && item.kind == KindProject {
				m.wtBranch = ""
				m.wtField = 0
				m.popupProj = item.project
				m.mode = viewNewWorktree
				return m, nil
			}
		}
		return m, nil
	}

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
	case "e":
		m.mode = viewList
		return m.updateList(msg)
	case "f":

		m.mode = viewList
		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" {
			m.toggleFavoriteGroup(item.group)
		}
		return m, nil
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) toggleFavoriteGroup(group string) {
	root := m.workspaceRootForGroup(group)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for group"
		return
	}
	current := false
	for i := range m.workspaces {
		if m.workspaces[i].Root == root {
			current = m.workspaces[i].FavoriteGroups[group]
			break
		}
	}
	target := !current
	err := MutateAndSave(root, func(ws *config.Workspace) bool {
		return ws.SetGroupFavorite(group, target)
	})
	if err != nil {
		m.statusMsg = "favorite: " + err.Error()
		return
	}
	for i := range m.workspaces {
		if m.workspaces[i].Root != root {
			continue
		}
		if m.workspaces[i].FavoriteGroups == nil {
			m.workspaces[i].FavoriteGroups = map[string]bool{}
		}
		if target {
			m.workspaces[i].FavoriteGroups[group] = true
			m.statusMsg = "* favorited @" + group
		} else {
			delete(m.workspaces[i].FavoriteGroups, group)
			m.statusMsg = "unfavorited @" + group
		}
		break
	}
	m.rebuildItems()
	m.clampCursor()
	m.ensureVisible()
}

func (m *Model) workspaceRootForGroup(name string) string {
	for _, ws := range m.workspaces {
		for _, g := range ws.Groups {
			if g == name {
				return ws.Root
			}
		}
	}
	return ""
}

func (m *Model) toggleFavoriteFor(proj *Project) {
	root := m.workspaceRootFor(proj)
	if root == "" {
		m.statusMsg = "cannot resolve workspace for project"
		return
	}
	target := !proj.Favorite
	err := MutateAndSave(root, func(ws *config.Workspace) bool {
		p := ws.Projects[proj.ID]
		if !p.SetFavorite(target) {
			return false
		}
		ws.Projects[proj.ID] = p
		return true
	})
	if err != nil {
		m.statusMsg = "favorite: " + err.Error()
		return
	}
	proj.Favorite = target
	if target {
		m.statusMsg = "* favorited " + proj.Name
	} else {
		m.statusMsg = "unfavorited " + proj.Name
	}
	m.rebuildItems()
	m.clampCursor()
	m.ensureVisible()
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
		return item.group
	}
	return "actions"
}

func (m *Model) viewWhichKey() string {
	listW := 48
	if m.width < 72 {
		listW = m.width - 28
		if listW < 30 {
			listW = 30
		}
	}

	var rows []string
	bc := m.breadcrumb()
	pos := fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
	hdr := m.padRight(" "+bc, pos+" ", listW)
	rows = append(rows, headerStyle.Width(listW).Render(hdr))
	rows = append(rows, m.renderListRows(listW, true)...)
	rows = append(rows, footerStyle.Width(listW).Render(" press a key or esc"))

	listPanel := tui.JoinVertical(tui.Left, rows...)

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

	listH := tui.Height(listPanel)
	panelH := tui.Height(actionPanel)
	topPad := (listH - panelH) / 2
	if topPad < 0 {
		topPad = 0
	}
	paddedPanel := strings.Repeat("\n", topPad) + actionPanel

	combined := tui.JoinHorizontal(tui.Top, listPanel, "  ", paddedPanel)

	return tui.Place(
		m.width, m.height,
		tui.Center, tui.Center,
		combined,
	)
}

func LaunchShell(cwd string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %s: %w", cwd, err)
	}

	return syscall.Exec(shell, []string{shell}, os.Environ())
}
