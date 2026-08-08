package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestNewModelUsesSeededInventoryWithoutInspectingRepositories(t *testing.T) {
	registry := time.Unix(100, 0)
	commit := time.Unix(200, 0)
	missing := filepath.Join(t.TempDir(), "missing")
	project := Project{
		Name:              "alpha",
		Path:              missing,
		BranchActivity:    map[string]time.Time{"main": registry},
		WorktreeInventory: []Worktree{{Path: missing, Branch: "main", IsMain: true, LastActiveAt: commit}, {Path: missing + "-wt", Branch: "feat/x"}},
	}

	m := NewModel([]WorkspaceData{{Projects: []Project{project}}})
	got := &m.workspaces[0].Projects[0]
	if got.WorktreeCount != 2 || !got.LastActiveAt.Equal(commit) {
		t.Fatalf("project inventory projection = count %d, recency %s", got.WorktreeCount, got.LastActiveAt)
	}
	m.openGlobalSearch()
	if len(m.items) != 3 {
		t.Fatalf("global search items = %d, want project plus two worktrees", len(m.items))
	}
	if details, err := m.wtCache.Get(missing); err != nil || len(details) != 0 {
		t.Fatalf("detail cache treated inventory as details: details=%v err=%v", details, err)
	}
}

func TestProjectRecencyUsesNewestRegistryOrCommit(t *testing.T) {
	older := time.Unix(100, 0)
	newer := time.Unix(200, 0)
	if got := projectRecency(older, []Worktree{{LastActiveAt: newer}}); !got.Equal(newer) {
		t.Fatalf("commit recency = %s, want %s", got, newer)
	}
	if got := projectRecency(newer, []Worktree{{LastActiveAt: older}}); !got.Equal(newer) {
		t.Fatalf("registry recency = %s, want %s", got, newer)
	}
}

func TestLoadWorkspaceIncludesPlainCheckoutInventoryAndCommitRecency(t *testing.T) {
	root := t.TempDir()
	repo := testutil.InitFakePlainCheckout(t, root, "alpha", nil)
	ws := &config.Workspace{Meta: config.Meta{Version: 1}, Projects: map[string]config.Project{
		"alpha": {Path: "alpha", Status: config.StatusActive},
	}, Groups: map[string]config.Group{}}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}

	loaded, diagnostics := loadOneWorkspace(root)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if loaded == nil || len(loaded.Projects) != 1 {
		t.Fatalf("loaded workspace = %#v", loaded)
	}
	p := loaded.Projects[0]
	if p.WorktreeCount != 1 || len(p.WorktreeInventory) != 1 || p.WorktreeInventory[0].Path != repo || !p.WorktreeInventory[0].IsMain || p.LastActiveAt.IsZero() {
		t.Fatalf("plain checkout project = %#v", p)
	}
}

func TestReloadProjectMetadataMakesRebuildUseLiveRegistryRecency(t *testing.T) {
	root := t.TempDir()
	active := time.Now().UTC().Truncate(time.Second)
	ws := &config.Workspace{Meta: config.Meta{Version: 1}, Projects: map[string]config.Project{
		"alpha": {Path: "alpha", DefaultBranch: "trunk", Branches: []config.BranchMeta{{Name: "feat/live", Machines: []string{"arch"}, LastActiveAt: active.Format(time.RFC3339), LastActiveMachine: "arch"}}},
	}, Groups: map[string]config.Group{}}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	m := &Model{workspaces: []WorkspaceData{{Root: root, Projects: []Project{{ID: "alpha", Name: "alpha", WorkspaceRoot: root}}}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache()}
	if err := m.reloadProjectMetadata(root, "alpha"); err != nil {
		t.Fatal(err)
	}
	m.rebuildItems()
	p := &m.workspaces[0].Projects[0]
	if !p.LastActiveAt.Equal(active) || p.LastActiveMachine != "arch" || p.DefaultBranch != "trunk" || !p.BranchActivity["feat/live"].Equal(active) {
		t.Fatalf("reloaded project = %+v", p)
	}
}

func TestProjectSheetLoadsFullDetailsInsteadOfInventory(t *testing.T) {
	repo := testutil.InitFakePlainCheckout(t, t.TempDir(), "repo", nil)
	testutil.AddDirty(t, repo)
	p := &Project{Name: "repo", Path: repo, WorktreeInventory: []Worktree{{Path: repo, IsMain: true}}}
	m := NewModel([]WorkspaceData{{Projects: []Project{*p}}})
	rows := buildProjectSheetRows(m, &m.workspaces[0].Projects[0])
	worktrees := filterByKind(rows, rowWorktree)
	if len(worktrees) != 1 || worktrees[0].hint != "dirty" {
		t.Fatalf("worktree rows = %#v, want freshly inspected dirty detail", worktrees)
	}
}

func TestRebuildItems_SortsProjectsAndGroupsByActivityDesc(t *testing.T) {
	now := time.Now()
	m := &Model{
		workspaces: []WorkspaceData{{
			Root:   "/ws",
			Groups: []string{"alpha", "beta"},
			Projects: []Project{
				{Name: "z", LastActiveAt: now.Add(-2 * time.Hour)},
				{Name: "a", LastActiveAt: now.Add(-30 * time.Minute)},
				{Name: "m"},
				{Name: "alpha-old", Group: "alpha", LastActiveAt: now.Add(-3 * time.Hour)},
				{Name: "beta-stale", Group: "beta", LastActiveAt: now.Add(-1 * time.Hour)},
				{Name: "beta-fresh", Group: "beta", LastActiveAt: now.Add(-10 * time.Minute)},
			},
		}},
		expanded: map[string]bool{groupKey("/ws", "beta"): true},
	}

	m.rebuildItems()

	var got []string
	for _, it := range m.items {
		switch it.kind {
		case KindProject:
			got = append(got, it.project.Name)
		case KindGroup:
			got = append(got, "@"+it.group)
		}
	}

	// Ungrouped first by activity desc (a, z, then activity-less m),
	// then groups by their freshest project (beta before alpha),
	// with beta expanded into its projects by activity desc.
	want := []string{"a", "z", "m", "@beta", "beta-fresh", "beta-stale", "@alpha"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecentViewSortsZeroActivityLastInBothOrders(t *testing.T) {
	for _, order := range []string{config.RecentOrderAsc, config.RecentOrderDesc} {
		m := &Model{
			workspaces:  []WorkspaceData{{Projects: []Project{{Name: "new", LastActiveAt: time.Unix(20, 0)}, {Name: "none"}, {Name: "old", LastActiveAt: time.Unix(10, 0)}}}},
			expanded:    map[string]bool{},
			homeView:    config.ExplorerViewRecent,
			recentOrder: order,
		}
		m.rebuildItems()
		got := []string{m.items[0].group, m.items[1].project.Name, m.items[2].project.Name, m.items[3].project.Name}
		want := []string{"Recent", "old", "new", "none"}
		if order == config.RecentOrderDesc {
			want = []string{"Recent", "new", "old", "none"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("order %s = %v, want %v", order, got, want)
		}
	}
}

func TestRecentViewFitsFixedHeightAcrossCanonicalGroups(t *testing.T) {
	projects := make([]Project, 30)
	groups := make([]string, len(projects))
	for i := range projects {
		groups[i] = fmt.Sprintf("group-%02d", i)
		projects[i] = Project{
			ID:            fmt.Sprintf("project-%02d", i),
			Name:          fmt.Sprintf("project-%02d", i),
			Group:         groups[i],
			Path:          fmt.Sprintf("/ws/project-%02d", i),
			LastActiveAt:  time.Unix(int64(1000-i), 0),
			WorkspaceRoot: "/ws",
			WorktreeCount: 1,
		}
	}
	m := NewModel([]WorkspaceData{{Root: "/ws", Groups: groups, Projects: projects}})
	m.homeView = config.ExplorerViewRecent
	m.width, m.height = 90, 18
	m.rebuildItems()

	assertRecentViewFits(t, m, "project-00", true)
	m.updateList(tui.KeyMsg{Type: tui.KeyCtrlF, Ctrl: true})
	selected := m.currentItem()
	if selected == nil || selected.project == nil || selected.project.Name == "project-00" {
		t.Fatalf("page motion did not move selection: %#v", selected)
	}
	assertRecentViewFits(t, m, selected.project.Name, false)
}

func assertRecentViewFits(t *testing.T, m *Model, selectedName string, expectHeading bool) {
	t.Helper()
	view := m.View()
	if got := tui.Height(view); got > m.height {
		t.Fatalf("rendered height = %d, model height = %d", got, m.height)
	}
	expected := []string{"^d/^u:half", selectedName}
	if expectHeading {
		expected = append(expected, "Recent")
	}
	for _, text := range expected {
		if !strings.Contains(view, text) {
			t.Fatalf("rendered view missing %q: %q", text, view)
		}
	}
}

func TestHomeVimMotionsClampAndRightOpensSheet(t *testing.T) {
	projects := make([]Project, 20)
	for i := range projects {
		projects[i] = Project{ID: fmt.Sprint(i), Name: fmt.Sprintf("p%02d", i), Path: fmt.Sprintf("/ws/p%02d", i)}
	}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: projects}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache(), homeView: config.ExplorerViewRecent, height: 15}
	m.rebuildItems()
	m.updateList(tui.KeyMsg{Type: tui.KeyEnd})
	if m.cursor != len(m.items)-1 || m.scroll == 0 {
		t.Fatalf("end = cursor %d scroll %d", m.cursor, m.scroll)
	}
	m.updateList(tui.KeyMsg{Type: tui.KeyCtrlF, Ctrl: true})
	if m.cursor != len(m.items)-1 {
		t.Fatalf("full page did not clamp: %d", m.cursor)
	}
	m.updateList(tui.KeyMsg{Type: tui.KeyHome})
	m.updateList(tui.KeyMsg{Type: tui.KeyCtrlD, Ctrl: true})
	if m.cursor == 0 || m.cursor >= m.listHeight() {
		t.Fatalf("half page cursor = %d", m.cursor)
	}
	m.updateList(tui.KeyMsg{Type: tui.KeyRight})
	if m.sheet == nil || m.Launch != nil {
		t.Fatalf("right should open selected project sheet: sheet=%v launch=%v", m.sheet, m.Launch)
	}
}

func TestRecentProjectionDefaultsExpandedAndCollapsesFromChild(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws"}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: []Project{p}}}, expanded: map[string]bool{}, homeView: config.ExplorerViewRecent}
	m.rebuildItems()
	if !m.expanded[recentKey()] || len(m.items) != 2 || !m.items[0].projectionGroup || m.items[1].expandKey != recentKey() {
		t.Fatalf("recent projection = %#v expanded=%v", m.items, m.expanded)
	}
	m.cursor = 1
	m.updateList(tui.KeyMsg{Type: tui.KeyLeft})
	if item := m.currentItem(); item == nil || item.kind != KindGroup || item.group != "Recent" || m.expanded[recentKey()] {
		t.Fatalf("item=%#v expanded=%v", item, m.expanded)
	}
}

func TestProjectionHeadingActionsAreSafe(t *testing.T) {
	m := &Model{expanded: map[string]bool{recentKey(): true}, homeView: config.ExplorerViewRecent}
	m.rebuildItems()
	m.updateList(tui.KeyMsg{Type: tui.KeyEnter})
	if m.sheet != nil {
		t.Fatalf("projection heading opened canonical sheet: %v", m.sheet)
	}
	for _, key := range []tui.KeyMsg{{Type: tui.KeyRunes, Runes: []rune("f")}, {Type: tui.KeyRunes, Runes: []rune("l")}, {Type: tui.KeyRunes, Runes: []rune("w")}, {Type: tui.KeyRunes, Runes: []rune("e")}} {
		m.updateList(key)
	}
	if m.sheet != nil || m.Launch != nil || m.mode != viewList || m.statusMsg != "" {
		t.Fatalf("projection action changed state: sheet=%v launch=%v mode=%v status=%q", m.sheet, m.Launch, m.mode, m.statusMsg)
	}
	actions, _ := m.footerHints()
	if strings.Contains(actions, "sheet") || strings.Contains(actions, "shell") {
		t.Fatalf("projection footer actions = %q", actions)
	}
	for _, action := range m.whichKeyActions() {
		if action.key == "f" || action.key == "l" || action.key == "d" || action.key == "m" {
			t.Fatalf("unsafe projection which-key action = %#v", action)
		}
	}
}

func TestLanguageProjectionDoesNotOpenCanonicalGroupSheet(t *testing.T) {
	m := &Model{
		workspaces: []WorkspaceData{{Root: "/ws", Projects: []Project{{Name: "alpha", Language: "Go"}}}},
		expanded:   map[string]bool{}, homeView: config.ExplorerViewLanguage,
	}
	m.rebuildItems()
	m.updateList(tui.KeyMsg{Type: tui.KeyEnter})
	if m.sheet != nil || !m.expanded[languageKey("Go")] {
		t.Fatalf("language projection opened canonical actions: sheet=%v expanded=%v", m.sheet, m.expanded)
	}
}

func TestGlobalSearchIncludesCollapsedWorktreeRendersAndRestoresHome(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Group: "org", Path: "/ws/alpha"}
	m := newTestModel(&p, []Worktree{{Path: "/ws/alpha-wt-feature", Branch: "feat/search"}})
	m.flashQuery = tui.NewTextInput()
	m.height = 20
	m.items = []listItem{{kind: KindGroup, group: "org"}, {kind: KindProject, project: &p}}
	m.cursor, m.scroll = 1, 1
	wantItems := append([]listItem(nil), m.items...)

	m.openGlobalSearch()
	var worktreeIndex = -1
	for i := range m.items {
		if m.items[i].kind == KindWorktree {
			worktreeIndex = i
		}
	}
	if worktreeIndex < 0 {
		t.Fatal("collapsed worktree missing from global search")
	}
	m.cursor = worktreeIndex
	if rendered := strings.Join(m.renderListRows(80, false), "\n"); !strings.Contains(rendered, "alpha › feat/search") {
		t.Fatalf("worktree row = %q", rendered)
	}
	m.updateFlash(tui.KeyMsg{Type: tui.KeyEsc})
	if m.cursor != 1 || m.scroll != 1 || !reflect.DeepEqual(m.items, wantItems) {
		t.Fatalf("home state not restored: cursor=%d scroll=%d items=%#v", m.cursor, m.scroll, m.items)
	}
}

func TestFlashForwardsTextInputControlKeysLocallyAndGlobally(t *testing.T) {
	for _, global := range []bool{false, true} {
		m := &Model{workspaces: []WorkspaceData{{Projects: []Project{{Name: "alphabet"}}}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache()}
		m.flashQuery = tui.NewTextInput()
		m.rebuildItems()
		if global {
			m.openGlobalSearch()
		} else {
			m.mode = viewFlash
			m.flashQuery.Focus()
		}
		m.flashQuery.SetValue("alphabet")
		m.flashQuery.CursorEnd()
		cursor := m.cursor
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlB, Ctrl: true})
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlF, Ctrl: true})
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlU, Ctrl: true})
		if m.flashQuery.Value() != "" || m.cursor != cursor {
			t.Fatalf("global=%v ctrl+u query=%q cursor=%d, want empty and %d", global, m.flashQuery.Value(), m.cursor, cursor)
		}
		m.flashQuery.SetValue("xy")
		m.flashQuery.CursorEnd()
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlB, Ctrl: true})
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlB, Ctrl: true})
		m.updateFlash(tui.KeyMsg{Type: tui.KeyCtrlD, Ctrl: true})
		if m.flashQuery.Value() != "y" || m.cursor != cursor {
			t.Fatalf("global=%v ctrl+d query=%q cursor=%d", global, m.flashQuery.Value(), m.cursor)
		}
	}
}

func TestGlobalSearchJumpLabelLaunchesExactWorktreePath(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "alpha")
	worktreePath := filepath.Join(root, "alpha-feature")
	for _, path := range []string{projectPath, worktreePath} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: root, Path: projectPath}
	m := newTestModel(&p, []Worktree{{Path: worktreePath, Branch: "feat/search"}})
	m.flashQuery = tui.NewTextInput()
	m.openGlobalSearch()
	m.flashQuery.SetValue("feat/search")
	m.recomputeFlash()
	if len(m.flashLabels) == 0 || m.flashLabels[0] == 0 {
		t.Fatal("worktree has no jump label")
	}
	m.updateFlash(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{m.flashLabels[0]}})
	if m.Launch == nil || m.Launch.Cwd != worktreePath {
		t.Fatalf("launch = %+v, want %s", m.Launch, worktreePath)
	}
}

func TestGlobalSearchProjectSelectionFocusesStableIdentity(t *testing.T) {
	projects := []Project{
		{ID: "first", Name: "first", WorkspaceRoot: "/ws", Path: "/ws/first"},
		{ID: "second", Name: "second", WorkspaceRoot: "/ws", Path: "/ws/second"},
	}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: projects}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache(), homeView: config.ExplorerViewRecent}
	m.flashQuery = tui.NewTextInput()
	m.rebuildItems()
	m.openGlobalSearch()
	m.flashQuery.SetValue("second")
	m.recomputeFlash()
	m.updateFlash(tui.KeyMsg{Type: tui.KeyEnter})
	if item := m.currentItem(); item == nil || item.project == nil || item.project.ID != "second" {
		t.Fatalf("focused item = %#v", item)
	}
}

func TestGlobalSearchProjectSelectionExpandsParentProjection(t *testing.T) {
	tests := []struct {
		name     string
		homeView string
		key      string
	}{
		{name: "projects", homeView: config.ExplorerViewProjects, key: groupKey("/ws", "org")},
		{name: "language", homeView: config.ExplorerViewLanguage, key: languageKey("Go")},
		{name: "recent", homeView: config.ExplorerViewRecent, key: recentKey()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Group: "org", Path: "/ws/alpha", Language: "Go"}
			m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Groups: []string{"org"}, Projects: []Project{project}}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache(), homeView: tt.homeView}
			m.flashQuery = tui.NewTextInput()
			m.rebuildItems()
			m.expanded[tt.key] = false
			m.rebuildItems()
			m.openGlobalSearch()
			m.flashQuery.SetValue("alpha")
			m.recomputeFlash()
			m.updateFlash(tui.KeyMsg{Type: tui.KeyEnter})
			if !m.expanded[tt.key] {
				t.Fatalf("parent projection %q remained collapsed", tt.key)
			}
			if item := m.currentItem(); item == nil || item.project == nil || item.workspaceRoot != "/ws" || item.project.ID != "alpha" {
				t.Fatalf("focused item = %#v", item)
			}
		})
	}
}

func TestLanguageProjectLeftReturnsToLanguageHeading(t *testing.T) {
	p := Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Group: "canonical", Language: "Go"}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: []Project{p}}}, expanded: map[string]bool{languageKey("Go"): true}, homeView: config.ExplorerViewLanguage}
	m.rebuildItems()
	m.cursor = 1
	m.updateList(tui.KeyMsg{Type: tui.KeyLeft})
	if item := m.currentItem(); item == nil || item.kind != KindGroup || item.group != "Go" || m.expanded[languageKey("Go")] {
		t.Fatalf("item=%#v expanded=%v", item, m.expanded)
	}
}
