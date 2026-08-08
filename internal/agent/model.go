package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
)

type NodeKind int

const (
	KindWorkspace NodeKind = iota
	KindGroup
	KindProject
	KindWorktree
	KindPortal
)

type Project struct {
	ID                string
	Name              string
	Group             string
	Category          string
	Path              string
	DefaultBranch     string
	WorktreeCount     int
	SessionCount      int
	Favorite          bool
	LastActiveAt      time.Time
	LastActiveMachine string
	BranchActivity    map[string]time.Time
	Language          string
}

func GroupPath(wsRoot, group string) string {
	return filepath.Join(wsRoot, group)
}

func canonicalGroupKey(root, group string) string {
	return "group:" + root + ":" + group
}

type WorkspaceData struct {
	Name           string
	Root           string
	Groups         []string
	Projects       []Project
	FavoriteGroups map[string]bool
}

type Chip struct {
	Kind         NodeKind
	Name         string
	Path         string
	Favorite     bool
	LastActiveAt time.Time

	Project *Project

	WorkspaceRoot string
}

func (m *Model) rebuildItems() {
	m.items = nil
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			m.refreshProjectRecency(&m.workspaces[wi].Projects[pi])
		}
	}
	m.headerChips = buildHeaderChips(m.workspaces)

	if m.homeView == config.ExplorerViewRecent {
		m.rebuildRecentItems()
		m.clampCursor()
		return
	}
	if m.homeView == config.ExplorerViewLanguage {
		m.rebuildLanguageItems()
		m.clampCursor()
		return
	}
	for _, ws := range m.workspaces {
		projects := make([]*Project, 0, len(ws.Projects))
		for i := range ws.Projects {
			projects = append(projects, &ws.Projects[i])
		}
		sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
		for _, p := range projects {
			if p.Group == "" {
				m.addProjectItem(p, 0)
			}
		}

		for _, g := range ws.Groups {
			key := canonicalGroupKey(ws.Root, g)
			m.items = append(m.items, listItem{kind: KindGroup, group: g, groupKind: groupCanonical, groupRoot: ws.Root, expandKey: key, indent: 0, path: GroupPath(ws.Root, g)})
			if m.expanded[key] {
				for _, p := range projects {
					if p.Group == g {
						m.addProjectItem(p, 1)
					}
				}
			}
		}
	}
	m.clampCursor()
}

func (m *Model) rebuildRecentItems() {
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			p := &m.workspaces[wi].Projects[pi]
			m.refreshProjectRecency(p)
			m.addProjectItem(p, 0)
		}
	}
	sort.Slice(m.items, func(i, j int) bool {
		return recencyLess(m.items[i].project.LastActiveAt, m.items[j].project.LastActiveAt, m.items[i].project.Name, m.items[j].project.Name, m.recentOrder == config.RecentOrderDesc)
	})
}

func (m *Model) rebuildLanguageItems() {
	groups := map[string][]*Project{}
	for wi := range m.workspaces {
		for pi := range m.workspaces[wi].Projects {
			p := &m.workspaces[wi].Projects[pi]
			groups[p.Language] = append(groups[p.Language], p)
		}
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		if name == "" {
			name = "Other"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key := "lang:" + name
		m.items = append(m.items, listItem{kind: KindGroup, group: name, groupKind: groupLanguage, expandKey: key})
		ps := groups[name]
		if name == "Other" && len(ps) == 0 {
			ps = groups[""]
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
		if m.expanded[key] {
			for _, p := range ps {
				m.addProjectItem(p, 1)
			}
		}
	}
}

func recencyLess(a, b time.Time, an, bn string, desc bool) bool {
	if a.Equal(b) {
		return an < bn
	}
	if a.IsZero() {
		return false
	}
	if b.IsZero() {
		return true
	}
	if desc {
		return a.After(b)
	}
	return a.Before(b)
}

func (m *Model) refreshProjectRecency(p *Project) {
	wts := m.wtCache.Get(p.Path)
	var latest time.Time
	for i := range wts {
		if active := p.BranchActivity[wts[i].Branch]; active.After(wts[i].LastActiveAt) {
			wts[i].LastActiveAt = active
		}
		if wts[i].LastActiveAt.After(latest) {
			latest = wts[i].LastActiveAt
		}
	}
	for _, active := range p.BranchActivity {
		if active.After(latest) {
			latest = active
		}
	}
	p.LastActiveAt = latest
	m.wtCache.data[p.Path] = wts
}

func (m *Model) clampCursor() {
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) addProjectItem(p *Project, indent int) {
	m.items = append(m.items, listItem{kind: KindProject, project: p, indent: indent, path: p.Path})
}

func StampLaunchFromPath(cwd string) error {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	wsRoot, ok := config.FindRootFrom(abs)
	if !ok {
		return nil
	}
	ws, err := config.Load(wsRoot)
	if err != nil {
		return nil
	}
	projID, proj := findProjectByPath(ws, wsRoot, abs)
	if proj == nil {
		return nil
	}
	branch, err := git.CurrentBranch(abs)
	if err != nil || branch == "" {
		return nil
	}
	machine := loadMachineName()
	if machine == "" {
		return nil
	}
	if !proj.StampActivity(branch, machine, time.Now()) {
		return nil
	}
	ws.Projects[projID] = *proj
	if err := config.Save(wsRoot, ws); err != nil {
		return err
	}
	return nil
}

func loadMachineName() string {
	mc, err := config.LoadMachineConfig()
	if err != nil || mc == nil {
		return ""
	}
	return mc.MachineName
}

func findProjectByPath(ws *config.Workspace, wsRoot, abs string) (string, *config.Project) {
	abs = filepath.Clean(abs)
	for id, p := range ws.Projects {
		projPath := filepath.Clean(filepath.Join(wsRoot, p.Path))
		if abs == projPath || strings.HasPrefix(abs, projPath+string(filepath.Separator)) {
			cp := p
			return id, &cp
		}
		wtPrefix := projPath + "-wt-"
		if abs == strings.TrimSuffix(wtPrefix, "-") {
			continue
		}
		if strings.HasPrefix(abs, wtPrefix) {
			cp := p
			return id, &cp
		}
	}
	return "", nil
}

const (
	iconGo         = ""
	iconRust       = ""
	iconPython     = ""
	iconNode       = ""
	iconTypeScript = ""
	iconJavaScript = ""
	iconRuby       = ""
	iconJava       = ""
	iconCSharp     = ""
	iconDocker     = ""
	iconShell      = ""
	iconMarkdown   = ""
)

var projectIconCache sync.Map

func DetectIcon(path string) string {
	if path == "" {
		return iconProject
	}
	if v, ok := projectIconCache.Load(path); ok {
		return v.(string)
	}
	icon := detectIconUncached(path)
	projectIconCache.Store(path, icon)
	return icon
}

var markerFiles = []struct {
	file string
	icon string
}{
	{"go.mod", iconGo},
	{"Cargo.toml", iconRust},
	{"pyproject.toml", iconPython},
	{"requirements.txt", iconPython},
	{"setup.py", iconPython},
	{"tsconfig.json", iconTypeScript},
	{"Gemfile", iconRuby},
	{"pom.xml", iconJava},
	{"build.gradle", iconJava},
	{"build.gradle.kts", iconJava},
	{"package.json", iconNode},
}

var suffixIcons = []struct {
	suffix string
	icon   string
}{
	{".csproj", iconCSharp},
	{".sln", iconCSharp},
	{".rs", iconRust},
	{".go", iconGo},
	{".ts", iconTypeScript},
	{".tsx", iconTypeScript},
	{".js", iconJavaScript},
	{".py", iconPython},
	{".rb", iconRuby},
	{".java", iconJava},
	{".cs", iconCSharp},
	{".sh", iconShell},
	{".bash", iconShell},
	{".zsh", iconShell},
}

func detectIconUncached(path string) string {
	for _, m := range markerFiles {
		if _, err := os.Stat(filepath.Join(path, m.file)); err == nil {
			return m.icon
		}
	}

	if _, err := os.Stat(filepath.Join(path, "Dockerfile")); err == nil {
		return iconDocker
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return iconProject
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		for _, s := range suffixIcons {
			if strings.HasSuffix(name, s.suffix) {
				return s.icon
			}
		}
	}

	if _, err := os.Stat(filepath.Join(path, "README.md")); err == nil {
		return iconMarkdown
	}
	return iconProject
}

func MutateAndSave(wsRoot string, apply func(*config.Workspace) bool) error {
	ws, err := config.Load(wsRoot)
	if err != nil {
		return err
	}
	if !apply(ws) {
		return nil
	}
	if err := config.Save(wsRoot, ws); err != nil {
		return err
	}
	return nil
}

func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, *SessionCache, []string) {
	var diagnostics []string
	roots := workspaceRoots(fallbackRoot)
	if len(roots) == 0 {
		diagnostics = append(diagnostics, "no workspace found; run from inside a workspace")
		return nil, nil, diagnostics
	}

	cache := NewSessionCache()
	var result []WorkspaceData
	for _, root := range roots {
		ws, diags := loadOneWorkspace(root, cache)
		diagnostics = append(diagnostics, diags...)
		if ws != nil {
			result = append(result, *ws)
		}
	}
	return result, cache, diagnostics
}

func loadOneWorkspace(root string, sessCache *SessionCache) (*WorkspaceData, []string) {
	var diagnostics []string
	w, err := config.Load(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", filepath.Base(root), err)}
	}

	ws := &WorkspaceData{
		Name:           filepath.Base(root),
		Root:           root,
		FavoriteGroups: map[string]bool{},
	}

	groupSet := map[string]bool{}
	names := make([]string, 0, len(w.Projects))
	for n, p := range w.Projects {
		if p.Status == config.StatusArchived {
			continue
		}
		names = append(names, n)
		if p.Group != "" {
			groupSet[p.Group] = true
		}
	}
	for g := range w.Groups {
		groupSet[g] = true
	}
	sort.Strings(names)
	for g := range groupSet {
		ws.Groups = append(ws.Groups, g)
		if entry, ok := w.Groups[g]; ok && entry.Favorite {
			ws.FavoriteGroups[g] = true
		}
	}
	sort.Strings(ws.Groups)

	for _, name := range names {
		p := w.Projects[name]
		mainPath := filepath.Join(root, p.Path)
		lastAt, lastMachine := projectActivity(p.Branches)
		proj := Project{
			ID:                name,
			Name:              name,
			Group:             p.Group,
			Category:          string(p.Category),
			Path:              mainPath,
			DefaultBranch:     p.DefaultBranch,
			Favorite:          p.Favorite,
			LastActiveAt:      lastAt,
			LastActiveMachine: lastMachine,
			BranchActivity:    branchActivity(p.Branches),
		}
		proj.Language = DetectLanguage(mainPath)

		barePath := layout.BarePath(mainPath)
		if _, err := os.Stat(barePath); err == nil {
			if wts, err := git.WorktreeList(barePath); err == nil {
				count := 0
				for _, wt := range wts {
					if !wt.Bare {
						count++
					}
				}
				proj.WorktreeCount = count
			}
		}

		proj.SessionCount = sessCache.Count(mainPath)

		ws.Projects = append(ws.Projects, proj)
	}

	return ws, diagnostics
}

func branchActivity(branches []config.BranchMeta) map[string]time.Time {
	result := map[string]time.Time{}
	for _, b := range branches {
		if t, err := time.Parse(time.RFC3339, b.LastActiveAt); err == nil {
			result[b.Name] = t
		}
	}
	return result
}

func DetectLanguage(path string) string {
	icon := DetectIcon(path)
	for _, v := range []struct{ icon, name string }{{iconGo, "Go"}, {iconRust, "Rust"}, {iconPython, "Python"}, {iconTypeScript, "TypeScript"}, {iconJavaScript, "JavaScript"}, {iconNode, "JavaScript"}, {iconRuby, "Ruby"}, {iconJava, "Java"}, {iconCSharp, "C#"}, {iconDocker, "Docker"}, {iconShell, "Shell"}, {iconMarkdown, "Markdown"}} {
		if icon == v.icon {
			return v.name
		}
	}
	return "Other"
}

func projectActivity(branches []config.BranchMeta) (time.Time, string) {
	var best time.Time
	var machine string
	for _, b := range branches {
		if b.LastActiveAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, b.LastActiveAt)
		if err != nil {
			continue
		}
		if t.After(best) {
			best = t
			machine = b.LastActiveMachine
		}
	}
	return best, machine
}

func workspaceRoots(fallback string) []string {
	if roots, err := config.ListWorkspaceRoots(); err == nil && len(roots) > 0 {
		return roots
	}
	if fallback != "" {
		if root, ok := config.FindRootFrom(fallback); ok {
			return []string{root}
		}
	}
	if root, err := config.FindRoot(); err == nil {
		return []string{root}
	}
	return nil
}
