package agent

import (
	"testing"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
)

func TestRefreshProjectRecencyUsesRegistryOrCommitMaximum(t *testing.T) {
	commitAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registryAt := commitAt.Add(time.Hour)
	p := &Project{Name: "alpha", Path: "/ws/alpha", BranchActivity: map[string]time.Time{"feat/x": registryAt}}
	m := newTestModel(p, []Worktree{{Path: "/ws/alpha-x", Branch: "feat/x", LastActiveAt: commitAt}}, nil)
	m.refreshProjectRecency(p)
	if !p.LastActiveAt.Equal(registryAt) || !m.wtCache.data[p.Path][0].LastActiveAt.Equal(registryAt) {
		t.Fatalf("recency = %v, want %v", p.LastActiveAt, registryAt)
	}
}

func TestRecentProjectionSortsDirectionZeroLastAndNameTies(t *testing.T) {
	now := time.Now()
	m := projectionTestModel([]Project{
		{Name: "z", LastActiveAt: now},
		{Name: "a", LastActiveAt: now},
		{Name: "zero"},
	})
	m.homeView, m.recentOrder = config.ExplorerViewRecent, config.RecentOrderDesc
	m.rebuildItems()
	assertItemNames(t, m.items, []string{"a", "z", "zero"})
	m.recentOrder = config.RecentOrderAsc
	m.rebuildItems()
	assertItemNames(t, m.items, []string{"a", "z", "zero"})
}

func TestProjectsAndLanguageProjections(t *testing.T) {
	m := projectionTestModel([]Project{
		{Name: "beta", Group: "team", Language: "Rust"},
		{Name: "alpha", Group: "team", Language: "Go"},
		{Name: "misc", Language: "Other"},
	})
	m.workspaces[0].Groups = []string{"team"}
	m.expanded[canonicalGroupKey("/ws", "team")] = true
	m.homeView = config.ExplorerViewProjects
	m.rebuildItems()
	assertItemNames(t, m.items, []string{"misc", "team", "alpha", "beta"})
	m.homeView = config.ExplorerViewLanguage
	m.expanded["lang:Go"] = true
	m.expanded["lang:Other"] = true
	m.expanded["lang:Rust"] = true
	m.rebuildItems()
	assertItemNames(t, m.items, []string{"Go", "alpha", "Other", "misc", "Rust", "beta"})
}

func TestProjectSheetWorktreesSortByRecencyThenName(t *testing.T) {
	now := time.Now()
	p := &Project{Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, []Worktree{
		{Path: "/ws/z", Branch: "z", LastActiveAt: now},
		{Path: "/ws/a", Branch: "a", LastActiveAt: now},
		{Path: "/ws/zero", Branch: "zero"},
	}, nil)
	rows := filterByKind(buildProjectSheetRows(m, p, map[string]bool{}), rowWorktree)
	if rows[0].wt.Branch != "a" || rows[1].wt.Branch != "z" || rows[2].wt.Branch != "zero" {
		t.Fatalf("worktree order = %q, %q, %q", rows[0].wt.Branch, rows[1].wt.Branch, rows[2].wt.Branch)
	}
}

func TestGlobalSearchFindsCollapsedOffscreenWorktreeAndFocusesFirst(t *testing.T) {
	projects := make([]Project, 20)
	for i := range projects {
		projects[i] = Project{Name: string(rune('a' + i)), Path: "/ws/p" + string(rune('a'+i))}
	}
	m := projectionTestModel(projects)
	target := &m.workspaces[0].Projects[19]
	m.wtCache.data[target.Path] = []Worktree{{Path: target.Path + "-hidden", Branch: "feat/needle"}}
	m.homeView = config.ExplorerViewProjects
	m.rebuildItems()
	m.cursor, m.scroll, m.savedCursor, m.savedScroll = 5, 5, 5, 5
	m.flashGlobal, m.mode, m.flashQuery = true, viewFlash, "needle"
	m.recomputeFlash()
	if len(m.items) != 1 || m.items[0].kind != KindWorktree || m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("search results = %+v cursor=%d scroll=%d", m.items, m.cursor, m.scroll)
	}
	m.exitFlash(false)
	if m.cursor != 5 || m.scroll != 5 {
		t.Fatalf("restored cursor/scroll = %d/%d", m.cursor, m.scroll)
	}
}

func projectionTestModel(projects []Project) *Model {
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: projects}}, expanded: map[string]bool{}, wtCache: NewWorktreeCache(), sessCache: NewSessionCache()}
	for _, p := range projects {
		m.wtCache.data[p.Path] = nil
	}
	return m
}

func assertItemNames(t *testing.T, items []listItem, want []string) {
	t.Helper()
	got := make([]string, 0, len(items))
	for _, item := range items {
		if item.project != nil {
			got = append(got, item.project.Name)
		} else {
			got = append(got, item.group)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("items = %v, want %v", got, want)
		}
	}
}
