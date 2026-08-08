package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestSameNamedGroupsStayIndependentAcrossWorkspaces(t *testing.T) {
	rootA := explorerWorkspace(t, "a")
	rootB := explorerWorkspace(t, "b")
	projectA := Project{ID: "a", Name: "a", WorkspaceRoot: rootA, Group: "shared", Path: filepath.Join(rootA, "shared", "a")}
	projectB := Project{ID: "b", Name: "b", WorkspaceRoot: rootB, Group: "shared", Path: filepath.Join(rootB, "shared", "b")}
	m := NewModel([]WorkspaceData{
		{Root: rootA, Groups: []string{"shared"}, Projects: []Project{projectA}, FavoriteGroups: map[string]bool{}},
		{Root: rootB, Groups: []string{"shared"}, Projects: []Project{projectB}, FavoriteGroups: map[string]bool{}},
	})

	m.toggleExpand(groupKey(rootA, "shared"))
	if m.expanded[groupKey(rootA, "shared")] || !m.expanded[groupKey(rootB, "shared")] {
		t.Fatalf("expansion crossed workspaces: %#v", m.expanded)
	}

	sheetA := newGroupSheet(m, rootA, "shared")
	if sheetA.groupPath != filepath.Join(rootA, "shared") {
		t.Fatalf("group path = %q", sheetA.groupPath)
	}
	projectRows := filterByKind(sheetA.rows, rowProject)
	if len(projectRows) != 1 || projectRows[0].proj.ID != "a" {
		t.Fatalf("group sheet projects = %#v", projectRows)
	}

	m.toggleFavoriteGroup(rootB, "shared")
	loadedA, err := config.Load(rootA)
	if err != nil {
		t.Fatal(err)
	}
	loadedB, err := config.Load(rootB)
	if err != nil {
		t.Fatal(err)
	}
	if loadedA.Groups["shared"].Favorite || !loadedB.Groups["shared"].Favorite {
		t.Fatal("favorite mutation targeted the wrong workspace")
	}
}

func TestExplorerLaunchContracts(t *testing.T) {
	root := explorerWorkspace(t, "launch")
	projectPath := filepath.Join(root, "project")
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Project{ID: "project", Name: "project", WorkspaceRoot: root, Path: projectPath, Favorite: true}
	m := NewModel([]WorkspaceData{{Root: root, Projects: []Project{p}}})

	m.updateList(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'1'}})
	if m.Launch == nil || m.Launch.Cwd != projectPath {
		t.Fatalf("digit launch = %+v", m.Launch)
	}
	m.Launch = nil
	m.cursor = 1
	m.updateList(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'l'}})
	if m.sheet == nil || m.Launch != nil {
		t.Fatalf("project open = sheet %v launch %+v", m.sheet, m.Launch)
	}
	m.sheet = nil
	m.Update(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'s'}, Ctrl: true})
	if m.Launch == nil || m.Launch.Cwd != projectPath {
		t.Fatalf("explicit shell launch = %+v", m.Launch)
	}

	outside := t.TempDir()
	m.Launch = nil
	m.launch(root, outside)
	if m.Launch != nil || !strings.Contains(m.statusMsg, "outside workspace") {
		t.Fatalf("outside launch was not blocked: launch=%+v status=%q", m.Launch, m.statusMsg)
	}
}

func TestProjectFavoritePersists(t *testing.T) {
	root := explorerWorkspace(t, "favorite")
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Projects["project"] = config.Project{Path: "project", Status: config.StatusActive}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	p := &Project{ID: "project", Name: "project", WorkspaceRoot: root, Path: filepath.Join(root, "project")}
	m := NewModel([]WorkspaceData{{Root: root, Projects: []Project{*p}}})
	m.toggleFavoriteFor(p)
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Projects["project"].Favorite {
		t.Fatal("project favorite was not persisted")
	}
}

func TestLoadWorkspaceSkipsPathsOutsideRoot(t *testing.T) {
	root := explorerWorkspace(t, "unsafe")
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Groups["../outside"] = config.Group{}
	workspace.Projects["unsafe"] = config.Project{Path: "../outside", Status: config.StatusActive}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	loaded, diagnostics := loadOneWorkspace(root)
	if loaded == nil {
		t.Fatal("workspace was not loaded")
	}
	if len(loaded.Projects) != 0 || len(loaded.Groups) != 1 {
		t.Fatalf("unsafe entries were loaded: projects=%#v groups=%#v", loaded.Projects, loaded.Groups)
	}
	if len(diagnostics) < 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSheetWorktreeLaunch(t *testing.T) {
	root := explorerWorkspace(t, "sheet")
	path := filepath.Join(root, "project-wt-feature")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{ID: "project", Name: "project", WorkspaceRoot: root, Path: filepath.Join(root, "project")}
	m := newTestModel(p, nil)
	s := newProjectSheet(m, p, nil)
	s.dispatchWorktree(m, &Worktree{Path: path, Branch: "feat/x"}, enter().String())
	if m.Launch == nil || m.Launch.Cwd != path {
		t.Fatalf("sheet worktree launch = %+v", m.Launch)
	}
}

func explorerWorkspace(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{
		Meta:     config.Meta{Version: 1},
		Groups:   map[string]config.Group{"shared": {}},
		Projects: map[string]config.Project{},
	}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	return root
}
