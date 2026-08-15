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
	WorktreeInventory []Worktree
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
	registered, err := findRegistryWorkspace(abs)
	if err != nil {
		return nil
	}
	projID, proj := findProjectByPath(registered.State, registered.Root, abs)
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
	return mutateRegistryWorkspace(registered.Root, func(workspace *config.Workspace) error {
		project, ok := workspace.Projects[projID]
		if !ok {
			return fmt.Errorf("project %q is missing from workspace registry", projID)
		}
		if !project.StampActivity(branch, machine, time.Now()) {
			return errRegistryUnchanged
		}
		workspace.Projects[projID] = project
		return nil
	})
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
	return mutateRegistryWorkspace(wsRoot, func(workspace *config.Workspace) error {
		if !apply(workspace) {
			return errRegistryUnchanged
		}
		return nil
	})
}

func (m *Model) reloadProjectMetadata(root, id string) error {
	ws, err := loadRegistryWorkspace(root)
	if err != nil {
		return err
	}
	metadata, ok := ws.Projects[id]
	if !ok {
		return fmt.Errorf("project %s is missing from registry", id)
	}
	for wi := range m.workspaces {
		if m.workspaces[wi].Root != root {
			continue
		}
		for pi := range m.workspaces[wi].Projects {
			project := &m.workspaces[wi].Projects[pi]
			if project.ID != id {
				continue
			}
			project.DefaultBranch = metadata.DefaultBranch
			project.BranchActivity = branchActivity(metadata.Branches)
			project.LastActiveAt, project.LastActiveMachine = projectActivity(metadata.Branches)
			return nil
		}
	}
	return fmt.Errorf("project %s is missing from explorer", id)
}

func LoadWorkspaces(fallbackRoot string) ([]WorkspaceData, []string) {
	_ = fallbackRoot
	registered, err := nodeWorkspaces()
	if err != nil {
		return nil, []string{err.Error()}
	}
	if len(registered) == 0 {
		return nil, []string{"no workspace found; create or import a workspace"}
	}

	var result []WorkspaceData
	var diagnostics []string
	for _, workspace := range registered {
		ws, diags := workspaceData(workspace.Root, workspace.State)
		diagnostics = append(diagnostics, diags...)
		if ws != nil {
			ws.Name = workspace.Name
			result = append(result, *ws)
		}
	}
	return result, diagnostics
}

func loadOneWorkspace(root string) (*WorkspaceData, []string) {
	w, err := loadRegistryWorkspace(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", filepath.Base(root), err)}
	}
	return workspaceData(root, w)
}

func workspaceData(root string, w *config.Workspace) (*WorkspaceData, []string) {
	var diagnostics []string
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

		proj.WorktreeInventory, err = LoadWorktreeInventory(mainPath)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s/%s: inspect worktrees: %s", filepath.Base(root), presentLabel(name), presentLabel(err.Error())))
		}
		proj.WorktreeCount = len(proj.WorktreeInventory)
		proj.LastActiveAt = projectRecency(proj.LastActiveAt, proj.WorktreeInventory)

		ws.Projects = append(ws.Projects, proj)
	}

	return ws, diagnostics
}

func projectRecency(registry time.Time, inventory []Worktree) time.Time {
	latest := registry
	for _, wt := range inventory {
		if wt.LastActiveAt.After(latest) {
			latest = wt.LastActiveAt
		}
	}
	return latest
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
