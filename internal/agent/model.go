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
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type NodeKind int

const (
	KindWorkspace NodeKind = iota
	KindGroup
	KindProject
	KindWorktree
)

type Project struct {
	ID                string
	Name              string
	WorkspaceRoot     string
	Group             string
	Category          string
	Path              string
	DefaultBranch     string
	WorktreeCount     int
	Favorite          bool
	LastActiveAt      time.Time
	LastActiveMachine string
	BranchActivity    map[string]time.Time
	Language          string
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
		projPath, err := layout.ProjectPath(wsRoot, p.Path)
		if err != nil {
			continue
		}
		projPath = filepath.Clean(projPath)
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

func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, []string) {
	var diagnostics []string
	roots := workspaceRoots(fallbackRoot)
	if len(roots) == 0 {
		diagnostics = append(diagnostics, "no workspace found; run from inside a workspace")
		return nil, diagnostics
	}

	var result []WorkspaceData
	for _, root := range roots {
		ws, diags := loadOneWorkspace(root)
		diagnostics = append(diagnostics, diags...)
		if ws != nil {
			result = append(result, *ws)
		}
	}
	return result, diagnostics
}

func loadOneWorkspace(root string) (*WorkspaceData, []string) {
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
			if _, err := layout.ProjectPath(root, p.Group); err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf("%s: skip group %q: %s", filepath.Base(root), presentLabel(p.Group), presentLabel(err.Error())))
			} else {
				groupSet[p.Group] = true
			}
		}
	}
	for g := range w.Groups {
		if _, err := layout.ProjectPath(root, g); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: skip group %q: %s", filepath.Base(root), presentLabel(g), presentLabel(err.Error())))
			continue
		}
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
		mainPath, err := layout.ProjectPath(root, p.Path)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: skip project %q: %s", filepath.Base(root), presentLabel(name), presentLabel(err.Error())))
			continue
		}
		if p.Group != "" && !groupSet[p.Group] {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: skip project %q: unsafe group", filepath.Base(root), presentLabel(name)))
			continue
		}
		lastAt, lastMachine := projectActivity(p.Branches)
		proj := Project{
			ID:                name,
			Name:              name,
			WorkspaceRoot:     root,
			Group:             p.Group,
			Category:          string(p.Category),
			Path:              mainPath,
			DefaultBranch:     p.DefaultBranch,
			Favorite:          p.Favorite,
			LastActiveAt:      lastAt,
			LastActiveMachine: lastMachine,
			BranchActivity:    branchActivity(p.Branches),
			Language:          languageForIcon(DetectIcon(mainPath)),
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
			} else {
				diagnostics = append(diagnostics, fmt.Sprintf("%s/%s: inspect worktrees: %s", filepath.Base(root), presentLabel(name), presentLabel(err.Error())))
			}
		} else if !os.IsNotExist(err) {
			diagnostics = append(diagnostics, fmt.Sprintf("%s/%s: inspect layout: %s", filepath.Base(root), presentLabel(name), presentLabel(err.Error())))
		}

		ws.Projects = append(ws.Projects, proj)
	}

	return ws, diagnostics
}

func branchActivity(branches []config.BranchMeta) map[string]time.Time {
	result := make(map[string]time.Time, len(branches))
	for _, branch := range branches {
		if at, err := time.Parse(time.RFC3339, branch.LastActiveAt); err == nil {
			result[branch.Name] = at
		}
	}
	return result
}

func languageForIcon(icon string) string {
	for _, value := range []struct{ icon, name string }{{iconGo, "Go"}, {iconRust, "Rust"}, {iconPython, "Python"}, {iconTypeScript, "TypeScript"}, {iconJavaScript, "JavaScript"}, {iconRuby, "Ruby"}, {iconJava, "Java"}, {iconCSharp, "C#"}, {iconDocker, "Docker"}, {iconShell, "Shell"}, {iconMarkdown, "Markdown"}} {
		if value.icon == icon {
			return value.name
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
