package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
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
}

func GroupPath(wsRoot, group string) string {
	return filepath.Join(wsRoot, group)
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
	m.headerChips = buildHeaderChips(m.workspaces)

	for _, ws := range m.workspaces {
		for i := range ws.Projects {
			p := &ws.Projects[i]
			if p.Group == "" {
				m.addProjectItem(p, 0)
			}
		}

		for _, g := range ws.Groups {
			m.items = append(m.items, listItem{kind: KindGroup, group: g, indent: 0, path: GroupPath(ws.Root, g)})
			if m.expanded[g] {
				for i := range ws.Projects {
					p := &ws.Projects[i]
					if p.Group == g {
						m.addProjectItem(p, 1)
					}
				}
			}
		}
	}
	m.clampCursor()
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

	if !m.expanded["proj:"+p.ID] {
		return
	}

	wts := m.wtCache.Get(p.Path)
	for i := range wts {
		wt := &wts[i]
		name := worktreeDisplayName(*wt)
		m.items = append(m.items, listItem{
			kind:       KindWorktree,
			worktree:   wt,
			indent:     indent + 1,
			path:       wt.Path,
			parentProj: p,
			group:      name,
		})
	}

	sessions := m.sessCache.Get(p.Path)
	if len(sessions) > 5 {
		sessions = sessions[:5]
	}
	for i := range sessions {
		s := &sessions[i]
		m.items = append(m.items, listItem{
			kind:       KindPortal,
			session:    s,
			indent:     indent + 1,
			path:       s.Cwd,
			parentProj: p,
		})
	}
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
	notifyDaemon(wsRoot)
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

func notifyDaemon(wsRoot string) {
	c, err := daemon.Dial()
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.Notify(wsRoot, "config_changed")
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
	notifyDaemon(wsRoot)
	return nil
}

func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, *SessionCache, []string) {
	var diagnostics []string
	roots := workspaceRoots(fallbackRoot)
	if len(roots) == 0 {
		diagnostics = append(diagnostics, "no workspaces registered (run `ws daemon register` or cd into a workspace)")
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
		}

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
	seen := map[string]bool{}
	var out []string

	cfg, err := daemon.LoadConfig()
	if err == nil && cfg != nil {
		for _, w := range cfg.Workspaces {
			if w.Root == "" || seen[w.Root] {
				continue
			}
			if _, err := os.Stat(filepath.Join(w.Root, "workspace.toml")); err != nil {
				continue
			}
			seen[w.Root] = true
			out = append(out, w.Root)
		}
	}

	if len(out) == 0 && fallback != "" {
		if _, err := os.Stat(filepath.Join(fallback, "workspace.toml")); err == nil {
			out = append(out, fallback)
		} else if root, err := config.FindRoot(); err == nil && !seen[root] {
			out = append(out, root)
		}
	}

	sort.Strings(out)
	return out
}
