package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/tui"
)

type viewMode int

const (
	viewList viewMode = iota
	viewNewWorktree
	viewFlash
	viewPromptInput
	viewWhichKey
	viewEditProject
	viewLifecycle
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
	groupKind  groupIdentity
	groupRoot  string
	expandKey  string
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
	workspaces  []WorkspaceData
	mode        viewMode
	items       []listItem
	cursor      int
	expanded    map[string]bool
	scroll      int
	homeView    string
	recentOrder string
	savedCursor int
	savedScroll int

	headerChips []Chip

	sheet *sheet

	sessCache *SessionCache
	wtCache   *WorktreeCache

	statusMsg string

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

	flashQuery   string
	flashMatches []int
	flashLabels  []rune
	flashGlobal  bool

	whichKeyLevel int
	lifecycle     *lifecycleModel

	Launch *LaunchRequest

	width, height int
}

func NewModel(workspaces []WorkspaceData, sessCache *SessionCache) *Model {
	if sessCache == nil {
		sessCache = NewSessionCache()
	}
	m := &Model{
		workspaces:  workspaces,
		mode:        viewList,
		expanded:    make(map[string]bool),
		sessCache:   sessCache,
		wtCache:     NewWorktreeCache(),
		homeView:    config.ExplorerViewRecent,
		recentOrder: config.RecentOrderDesc,
	}
	if mc, err := config.LoadMachineConfig(); err == nil {
		m.homeView = mc.ExplorerView
		m.recentOrder = mc.RecentOrder
	}

	for _, ws := range workspaces {
		for _, g := range ws.Groups {
			m.expanded[canonicalGroupKey(ws.Root, g)] = true
		}
	}
	for _, language := range []string{"Go", "Rust", "Python", "TypeScript", "JavaScript", "Ruby", "Java", "C#", "Docker", "Shell", "Markdown", "Other"} {
		m.expanded["lang:"+language] = true
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
				m.Launch = &LaunchRequest{Cwd: item.path, ShellOnly: true}
				return m, tui.Quit
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
		if m.mode == viewLifecycle {
			return m.updateLifecycle(msg)
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
	if m.mode == viewPromptInput {
		return m.viewPromptInput()
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

type groupIdentity int

const (
	groupCanonical groupIdentity = iota
	groupLanguage
)

func (m *Model) jumpToGroup(root, group string) {
	for i, it := range m.items {
		if it.kind == KindGroup && it.groupKind == groupCanonical && it.groupRoot == root && it.group == group {
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
	nav = "j/k:↕  1-9:chip  v:view  o:order  S:search  ?:more"
	item := m.currentItem()
	if item == nil {
		return "⏎:open  s:find  S:all", nav
	}
	switch item.kind {
	case KindGroup:
		actions = "⏎:sheet  tab:expand  p:+prompt  l:shell"
	case KindProject:
		actions = "⏎:sheet  p:+prompt  w:worktree  e:edit  l:shell"
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

func (m *Model) updateList(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	m.statusMsg = ""
	item := m.currentItem()

	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < len(m.headerChips) {
			m.openSheetForChip(m.headerChips[idx])
			return m, nil
		}
	}

	switch msg.String() {
	case "q":
		return m, tui.Quit
	case "A":
		m.openLifecycle(lifecycleScope{kind: lifecycleGlobal})
		return m, nil
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
			if item.groupKind == groupCanonical {
				m.sheet = newGroupSheet(m, item.groupRoot, item.group)
			} else {
				m.toggleExpand(item.expandKey)
			}
			return m, nil
		case KindProject:
			m.sheet = newProjectSheet(m, item.project, nil)
			return m, nil
		case KindWorktree:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tui.Quit
		case KindPortal:
			if item.session != nil {
				m.Launch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
				return m, tui.Quit
			}
		}

	case "p":

		if item != nil && item.path != "" && (item.kind == KindGroup || item.kind == KindProject || item.kind == KindWorktree) {
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		}

		if item != nil && item.kind == KindPortal && item.session != nil {
			m.pendingLaunch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		}

	case "w":

		if item != nil && item.kind == KindProject {
			m.wtNoLaunch = true
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
			m.Launch = &LaunchRequest{Cwd: item.path, ShellOnly: true}
			return m, tui.Quit
		}

	case "f":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.groupKind == groupCanonical {
			m.toggleFavoriteGroup(item.groupRoot, item.group)
		}

	case "h", "left":
		if item != nil {
			switch {
			case m.homeView == config.ExplorerViewProjects && item.kind == KindProject && item.project.Group != "":
				root := m.workspaceRootFor(item.project)
				m.expanded[canonicalGroupKey(root, item.project.Group)] = false
				m.rebuildItems()
				m.jumpToGroup(root, item.project.Group)
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

	case "d":
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			m.openWorktreeDelete(item.parentProj, item.worktree)
			return m, nil
		}

	case "a":
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			m.openWorktreeArchive(item.parentProj, item.worktree)
			return m, nil
		}
		if item != nil && item.kind == KindProject && item.project != nil {
			m.openLifecycle(lifecycleScope{kind: lifecycleProject, project: item.project})
			m.lifecycle.action = lifecycleArchiveProjects
			m.prepareLifecycle()
			return m, nil
		}
		if item != nil && item.kind == KindGroup && item.groupKind == groupCanonical {
			m.openLifecycle(lifecycleScope{kind: lifecycleGroup, group: item.group, workspaceRoot: item.groupRoot})
			m.lifecycle.action = lifecycleArchiveProjects
			m.prepareLifecycle()
			return m, nil
		}

	case "s", "/":
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "S":
		m.flashGlobal = true
		m.savedCursor, m.savedScroll = m.cursor, m.scroll
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

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
	case "o":
		if m.recentOrder == config.RecentOrderDesc {
			m.recentOrder = config.RecentOrderAsc
		} else {
			m.recentOrder = config.RecentOrderDesc
		}
		m.saveExplorerPreferences()
		m.rebuildItems()

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
	Path         string
	Branch       string
	IsMain       bool
	Dirty        bool
	Ahead        int
	LastActiveAt time.Time
}

func (m *Model) saveExplorerPreferences() {
	mc, err := config.LoadMachineConfig()
	if err == nil {
		mc.ExplorerView, mc.RecentOrder = m.homeView, m.recentOrder
		err = config.SaveMachineConfig(mc)
	}
	if err != nil {
		m.statusMsg = "preferences: " + err.Error()
	}
}

type WorktreeResult struct {
	Path   string
	Branch string
}

func CreateWorktree(p *Project, branch, wsRoot, projID string) (*WorktreeResult, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, fmt.Errorf("branch name required")
	}
	barePath := layout.BarePath(p.Path)
	if _, err := os.Stat(barePath); err != nil {
		return nil, fmt.Errorf("project not migrated (no bare repo at %s)", barePath)
	}

	mc, _ := config.LoadMachineConfig()
	machine := "unknown"
	if mc != nil && mc.MachineName != "" {
		machine = mc.MachineName
	}

	if !git.HasFetchRefspec(barePath) {
		_ = git.SetFetchRefspec(barePath)
	}

	_ = git.FetchRefspec(barePath, "origin", branch)
	localExists := git.HasBranch(barePath, branch)
	remoteExists := git.HasRemoteBranch(barePath, "origin", branch)

	if existingPath := findWorktreeForBranch(barePath, branch); existingPath != "" {
		if wsRoot != "" && projID != "" {
			if ws, err := config.Load(wsRoot); err == nil {
				if proj, ok := ws.Projects[projID]; ok {
					proj.ClaimBranch(branch, machine)
					if remoteExists {
						proj.MarkPushed(branch, machine, time.Now())
					}
					ws.Projects[projID] = proj
					if err := config.Save(wsRoot, ws); err != nil {
						return nil, fmt.Errorf("registry update failed: %w", err)
					}
				}
			}
		}
		return &WorktreeResult{Path: existingPath, Branch: branch}, nil
	}

	wtPath := layout.WorktreePathForBranch(p.Path, machine, branch)
	if _, err := os.Stat(wtPath); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", wtPath)
	}

	attachedToRemote := false
	switch {
	case localExists:

		if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
		attachedToRemote = remoteExists
	case remoteExists:

		if err := git.WorktreeAdd(barePath, wtPath, branch, "origin/"+branch); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
		attachedToRemote = true
	default:
		base := p.DefaultBranch
		if base == "" {
			base = "main"
		}
		if err := git.WorktreeAdd(barePath, wtPath, branch, base); err != nil {
			return nil, fmt.Errorf("git worktree add: %w", err)
		}
	}

	if wsRoot != "" && projID != "" {
		if ws, err := config.Load(wsRoot); err == nil {
			if proj, ok := ws.Projects[projID]; ok {
				proj.ClaimBranch(branch, machine)
				if attachedToRemote {
					proj.MarkPushed(branch, machine, time.Now())
				}
				ws.Projects[projID] = proj
				if err := config.Save(wsRoot, ws); err != nil {
					return nil, fmt.Errorf("worktree created but workspace.toml save failed: %w", err)
				}
			}
		}
	}

	return &WorktreeResult{Path: wtPath, Branch: branch}, nil
}

func findWorktreeForBranch(barePath, branch string) string {
	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return ""
	}
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		if wt.Branch == branch {
			return wt.Path
		}
	}
	return ""
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
		wt := Worktree{Path: mainPath, IsMain: true, Dirty: git.IsDirty(mainPath)}
		wt.Branch, _ = git.CurrentBranch(mainPath)
		wt.LastActiveAt, _ = git.LastCommitTime(mainPath)
		return []Worktree{wt}
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		wt := Worktree{Path: mainPath, IsMain: true, Dirty: git.IsDirty(mainPath)}
		wt.Branch, _ = git.CurrentBranch(mainPath)
		wt.LastActiveAt, _ = git.LastCommitTime(mainPath)
		return []Worktree{wt}
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
		if commitAt, err := git.LastCommitTime(wt.Path); err == nil {
			w.LastActiveAt = commitAt
		}
		ahead, _, _ := git.AheadBehind(wt.Path, wt.Branch)
		w.Ahead = ahead
		result = append(result, w)
	}
	return result
}

type Session struct {
	ID      string
	Title   string
	Cwd     string
	Updated time.Time
}

func LoadSessions(paths []string) []Session {
	claudeRoot := claudeProjectsDir()
	if claudeRoot == "" {
		return nil
	}

	pathLookup := make(map[string]string, len(paths))
	for _, p := range paths {
		encoded := encodeCwd(p)
		pathLookup[encoded] = p
	}

	var sessions []Session

	entries, err := os.ReadDir(claudeRoot)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		origPath, ok := pathLookup[entry.Name()]
		if !ok {
			continue
		}

		dirPath := filepath.Join(claudeRoot, entry.Name())
		files, err := filepath.Glob(filepath.Join(dirPath, "*.jsonl"))
		if err != nil {
			continue
		}

		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			info, err := os.Stat(f)
			if err != nil {
				continue
			}

			title := extractTitle(f)
			if title == "" {
				title = "(untitled)"
			}

			sessions = append(sessions, Session{
				ID:      id,
				Title:   title,
				Cwd:     origPath,
				Updated: info.ModTime(),
			})
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Updated.After(sessions[j].Updated)
	})
	return sessions
}

func extractTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"type":"user"`) {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "user" {
			continue
		}

		var text string
		if err := json.Unmarshal(entry.Message.Content, &text); err != nil {
			var parts []struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(entry.Message.Content, &parts); err == nil && len(parts) > 0 {
				text = parts[0].Text
			}
		}
		if len(text) > 60 {
			text = text[:57] + "…"
		}
		return text
	}
	return ""
}

func encodeCwd(path string) string {
	return strings.ReplaceAll(path, "/", "-")
}

func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

func FindSession(id string) *Session {
	claudeRoot := claudeProjectsDir()
	if claudeRoot == "" {
		return nil
	}

	entries, err := os.ReadDir(claudeRoot)
	if err != nil {
		return nil
	}

	target := id + ".jsonl"
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		f := filepath.Join(claudeRoot, entry.Name(), target)
		info, err := os.Stat(f)
		if err != nil {
			continue
		}

		cwd := strings.ReplaceAll(entry.Name(), "-", "/")

		if _, err := os.Stat(cwd); err != nil {
			continue
		}
		return &Session{
			ID:      id,
			Title:   extractTitle(f),
			Cwd:     cwd,
			Updated: info.ModTime(),
		}
	}
	return nil
}

type SessionCache struct {
	data map[string][]Session
}

func NewSessionCache() *SessionCache {
	return &SessionCache{data: make(map[string][]Session)}
}

func (c *SessionCache) Get(mainPath string) []Session {
	if sessions, ok := c.data[mainPath]; ok {
		return sessions
	}
	sessions := LoadSessions([]string{mainPath})
	c.data[mainPath] = sessions
	return sessions
}

func (c *SessionCache) Count(mainPath string) int {
	return len(c.Get(mainPath))
}

func (c *SessionCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

func TimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
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
	result, err := CreateWorktree(m.popupProj, branch, wsRoot, m.popupProj.ID)
	if err != nil {
		m.statusMsg = err.Error()
		m.mode = viewList
		return m, nil
	}
	m.wtCache.Invalidate(m.popupProj.Path)

	if m.wtNoLaunch {
		m.wtNoLaunch = false
		m.mode = viewList
		m.rebuildItems()
		m.ensureVisible()
		m.statusMsg = "worktree created"
		return m, nil
	}

	m.pendingLaunch = &LaunchRequest{Cwd: result.Path}
	m.promptInput = ""
	m.mode = viewPromptInput
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

func (m *Model) updatePromptInput(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = viewList
		m.pendingLaunch = nil
	case "enter":

		m.pendingLaunch.Prompt = strings.TrimSpace(m.promptInput)
		m.Launch = m.pendingLaunch
		m.pendingLaunch = nil
		return m, tui.Quit
	case "backspace":
		if len(m.promptInput) > 0 {
			m.promptInput = m.promptInput[:len(m.promptInput)-1]
		}
	default:
		if len(key) == 1 && key[0] >= 32 {
			m.promptInput += key
		} else if key == "space" || key == " " {
			m.promptInput += " "
		}
	}
	return m, nil
}

func (m *Model) viewPromptInput() string {
	if m.pendingLaunch == nil {
		m.mode = viewList
		return m.viewList()
	}
	popupW := 56
	if m.width < 62 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render("Launch claude"))
	lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("in: %s", m.pendingLaunch.Cwd)))
	lines = append(lines, "")
	lines = append(lines, popupItemStyle.Width(innerW).Render("  Initial prompt (optional):"))

	input := m.promptInput + "█"
	lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+input))
	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Enter: launch (empty = interactive)"))
	lines = append(lines, popupDimStyle.Width(innerW).Render("  Esc: back"))

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
		if item.groupKind == groupLanguage {
			return []whichKeyAction{
				{"⏎", "expand"},
				{"tab", "expand"},
				{"", ""},
				{"esc", "close"},
			}
		}
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"p", "+prompt"},
			{"f", m.favoriteToggleLabelGroup(item.groupRoot, item.group)},
			{"l", "shell"},
			{"tab", "expand"},
			{"", ""},
			{"esc", "close"},
		}
	case KindProject:
		return []whichKeyAction{
			{"⏎", "open sheet"},
			{"p", "+prompt"},
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

func (m *Model) favoriteToggleLabelGroup(root, group string) string {
	for _, ws := range m.workspaces {
		if ws.Root == root && ws.FavoriteGroups[group] {
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
				m.wtNoLaunch = true
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
		if item != nil && item.kind == KindGroup && item.groupKind == groupCanonical {
			m.toggleFavoriteGroup(item.groupRoot, item.group)
		}
		return m, nil
	case "tab":
		m.mode = viewList
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) toggleFavoriteGroup(root, group string) {
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
	case KindPortal:
		if item.session != nil {
			t := item.session.Title
			if len(t) > 16 {
				t = t[:16] + "…"
			}
			return t
		}
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

func LaunchClaude(cwd, resumeID, prompt string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	args := []string{"claude"}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	if prompt != "" {
		args = append(args, "-p", prompt)
	}

	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %s: %w", cwd, err)
	}

	return syscall.Exec(bin, args, os.Environ())
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
