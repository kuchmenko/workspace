package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/testutil"
	"github.com/kuchmenko/workspace/internal/tui"
)

func newTestModel(p *Project, wts []Worktree) *Model {
	m := &Model{
		workspaces: []WorkspaceData{{Root: "/ws", Projects: []Project{*p}}},
		expanded:   map[string]bool{},
		wtCache:    NewWorktreeCache(),
	}
	m.wtCache.details[p.Path] = wts
	m.wtCache.inventory[p.Path] = wts
	return m
}

func TestBuildProjectSheetRows_WorktreesSection(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	active := time.Now().Add(-2 * time.Hour)
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true, LastActiveAt: active},
		{Path: "/ws/alpha-wt-x", Branch: "wt/x", LastActiveAt: active},
		{Path: "/ws/alpha-wt-y", Branch: "wt/y", Dirty: true, Ahead: 3, LastActiveAt: active},
	}
	m := newTestModel(p, wts)

	rows := buildProjectSheetRows(m, p)

	wtRows := filterByKind(rows, rowWorktree)
	if len(wtRows) != 3 {
		t.Fatalf("worktree rows = %d, want 3", len(wtRows))
	}
	// Main always first.
	if !strings.Contains(wtRows[0].label, "main") {
		t.Errorf("first wt row label = %q, want main first", wtRows[0].label)
	}
	if wtRows[2].hint != "dirty ↑3" {
		t.Errorf("dirty+ahead hint = %q, want %q", wtRows[2].hint, "dirty ↑3")
	}
	if wtRows[2].activity != "2h" {
		t.Errorf("activity = %q, want 2h", wtRows[2].activity)
	}
	if rows[0].hint != "status" || rows[0].activity != "activity" {
		t.Fatalf("worktree columns = status %q activity %q", rows[0].hint, rows[0].activity)
	}
}

func TestBuildProjectSheetRowsSortsByRegistryOrCommitRecency(t *testing.T) {
	commitNewest := time.Unix(300, 0)
	registryNewest := time.Unix(400, 0)
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha", BranchActivity: map[string]time.Time{"feat/registry": registryNewest}}
	wts := []Worktree{
		{Path: "/ws/alpha-old", Branch: "feat/registry", LastActiveAt: time.Unix(100, 0)},
		{Path: "/ws/alpha-new", Branch: "feat/commit", LastActiveAt: commitNewest},
	}
	m := newTestModel(p, wts)

	rows := filterByKind(buildProjectSheetRows(m, p), rowWorktree)
	if len(rows) != 2 || rows[0].wt.Branch != "feat/registry" || !rows[0].wt.LastActiveAt.Equal(registryNewest) {
		t.Fatalf("sorted worktrees = %#v, want registry-active branch first", rows)
	}
}

func TestWorktreeInventoryReloadsAfterInvalidation(t *testing.T) {
	repo := testutil.InitFakePlainCheckout(t, t.TempDir(), "repo", nil)
	cache := NewWorktreeCache()
	cache.SeedInventory(repo, []Worktree{{Path: repo + "-stale"}})
	cache.Invalidate(repo)

	wts := cache.Inventory(repo)
	if len(wts) != 1 || wts[0].Path != repo || !wts[0].IsMain || wts[0].LastActiveAt.IsZero() {
		t.Fatalf("reloaded inventory = %#v", wts)
	}
}

func TestSheetFilter_KeepsSectionHeadersOnlyWhenSectionMatches(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-perf", Branch: "wt/perf-render"},
	}
	m := newTestModel(p, wts)
	s := newProjectSheet(m, p, nil)

	s.filter.SetValue("perf")
	s.applyFilter()

	for _, idx := range s.visible {
		r := s.rows[idx]
		if r.kind == rowHeader && !strings.Contains(r.label, "worktrees") {
			t.Errorf("unexpected surviving header: %q", r.label)
		}
	}
}

func TestSheetFilter_BackspaceRemovesCompleteMultibyteRune(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil)
	s := newProjectSheet(m, p, nil)
	s.filterMode = true
	s.filter.SetValue("café")
	s.filter.Focus()

	s.updateFilterMode(m, tui.KeyMsg{Type: tui.KeyBackspace})

	if got := s.filter.Value(); got != "caf" {
		t.Fatalf("filter = %q, want %q", got, "caf")
	}
}

func TestBuildGroupSheetRows_SortsProjectsByActivityDescThenName(t *testing.T) {
	now := time.Now()
	m := &Model{
		workspaces: []WorkspaceData{{
			Root:   "/ws",
			Groups: []string{"org"},
			Projects: []Project{
				{Name: "z", Group: "org", LastActiveAt: now.Add(-2 * time.Hour)},
				{Name: "a", Group: "org", LastActiveAt: now.Add(-30 * time.Minute)},
				{Name: "m", Group: "org"},
				{Name: "out", Group: "other"},
			},
		}},
	}

	rows := buildGroupSheetRows(m, "/ws", "org", "/ws/org")

	var projectNames []string
	for _, r := range rows {
		if r.kind == rowProject {
			projectNames = append(projectNames, r.label)
		}
	}
	want := []string{"a", "z", "m"}
	if len(projectNames) != len(want) {
		t.Fatalf("project rows = %v, want %v", projectNames, want)
	}
	for i, w := range want {
		if projectNames[i] != w {
			t.Errorf("[%d] = %q, want %q", i, projectNames[i], w)
		}
	}

}

func TestSheet_EscPopsToParent(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil)
	parent := newGroupSheet(m, "/ws", "org")
	child := newProjectSheet(m, p, parent)
	m.sheet = child

	child.update(m, esc())
	if m.sheet != parent {
		t.Errorf("esc from child sheet should pop to parent; got %p, want %p", m.sheet, parent)
	}

	parent.update(m, esc())
	if m.sheet != nil {
		t.Errorf("esc from root sheet should close to tree; got %p", m.sheet)
	}
}

func TestSheetVimMotionsAndRightOpen(t *testing.T) {
	projects := make([]Project, 30)
	for i := range projects {
		projects[i] = Project{ID: fmt.Sprint(i), Name: fmt.Sprintf("p%02d", i), Group: "org", Path: fmt.Sprintf("/ws/p%02d", i)}
	}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Groups: []string{"org"}, Projects: projects}}, wtCache: NewWorktreeCache()}
	s := newGroupSheet(m, "/ws", "org")
	m.sheet = s

	m.height = 40
	s.update(m, tui.KeyMsg{Type: tui.KeyCtrlF, Ctrl: true})
	if s.cursor == 0 || s.focused() == nil {
		t.Fatalf("full page cursor = %d row=%#v", s.cursor, s.focused())
	}
	s.update(m, tui.KeyMsg{Type: tui.KeyEnd})
	last := s.cursor
	s.update(m, tui.KeyMsg{Type: tui.KeyCtrlD, Ctrl: true})
	if s.cursor != last {
		t.Fatalf("half page did not clamp: got %d want %d", s.cursor, last)
	}
	s.update(m, tui.KeyMsg{Type: tui.KeyHome})
	for i, idx := range s.visible {
		if s.rows[idx].kind == rowProject {
			s.cursor = i
			break
		}
	}
	s.update(m, tui.KeyMsg{Type: tui.KeyRight})
	if m.sheet == nil || m.sheet == s || m.sheet.parent != s {
		t.Fatalf("right did not open project child sheet: %#v", m.sheet)
	}
	m.sheet.update(m, tui.KeyMsg{Type: tui.KeyLeft})
	if m.sheet != s {
		t.Fatalf("left did not return to parent: %#v", m.sheet)
	}
}

func TestNarrowSheetKeepsChromeAndSelectionVisible(t *testing.T) {
	projects := make([]Project, 24)
	for i := range projects {
		projects[i] = Project{ID: fmt.Sprint(i), Name: fmt.Sprintf("%02d-", i) + strings.Repeat("界-long-project-", 4), Group: strings.Repeat("long-group-", 4), Path: "/very/long/path/that/must/not/wrap"}
	}
	group := projects[0].Group
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Groups: []string{group}, Projects: projects}}, wtCache: NewWorktreeCache(), width: 42, height: 16}
	s := newGroupSheet(m, "/ws", group)
	s.statusMsg = strings.Repeat("status must stay one line ", 5)
	s.moveCursor(12)
	selected := s.focused().label
	view := s.view(m)
	if tui.Height(view) != m.height {
		t.Fatalf("height = %d, want %d", tui.Height(view), m.height)
	}
	if !strings.Contains(view, "@long-group") || !strings.Contains(view, "⏎/l:open") || !strings.Contains(view, selected[:3]) {
		t.Fatalf("title, footer, or selected item missing: %q", view)
	}
}

func TestSheetFilterModeKeepsTextInputControlKeys(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, []Worktree{{Path: "/ws/alpha", IsMain: true}, {Branch: "one"}, {Branch: "two"}})
	s := newProjectSheet(m, p, nil)
	s.filterMode = true
	s.filter.Focus()
	s.cursor = 3
	s.update(m, tui.KeyMsg{Type: tui.KeyCtrlU, Ctrl: true})
	if s.cursor != 3 {
		t.Fatalf("filter control key moved list cursor to %d", s.cursor)
	}
}

func TestProjectSheetFooterAlwaysShowsLifecycleAndProjectActions(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, []Worktree{{Path: "/ws/alpha", IsMain: true}, {Path: "/ws/alpha-wt", Branch: "feat/x"}})
	s := newProjectSheet(m, p, nil)
	for i, idx := range s.visible {
		if row := s.rows[idx]; row.kind == rowWorktree && row.wt != nil && !row.wt.IsMain {
			s.cursor = i
			break
		}
	}
	actions, nav := s.footerHints()
	for _, hint := range []string{"a:archive", "d:delete", "A:maint", "w:new", "e:edit", "f:fav", "/:filter"} {
		if !strings.Contains(actions, hint) {
			t.Errorf("actions missing %q: %q", hint, actions)
		}
	}
	if !strings.Contains(nav, "h:back") || !strings.Contains(nav, "g/G:first/last") {
		t.Fatalf("navigation hints = %q", nav)
	}
	selected := s.renderRow(s.cursor, 80)
	if !strings.Contains(selected, "clean") || tui.Width(selected) != 80 {
		t.Fatalf("selected row lost its right-hand status: width=%d row=%q", tui.Width(selected), selected)
	}
}

func TestSheetUppercaseAOpensScopedMaintenance(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", WorkspaceRoot: "/ws", Group: "org", Path: "/ws/alpha"}
	m := newTestModel(p, nil)

	projectSheet := newProjectSheet(m, p, nil)
	projectSheet.update(m, rune1('A'))
	if m.lifecycle == nil || m.lifecycle.scope.kind != lifecycleProject || m.lifecycle.scope.project != p || m.lifecycle.action != lifecycleChoose {
		t.Fatalf("project maintenance = %#v", m.lifecycle)
	}

	m.lifecycle, m.mode = nil, viewList
	groupSheet := newGroupSheet(m, "/ws", "org")
	groupSheet.update(m, rune1('A'))
	if m.lifecycle == nil || m.lifecycle.scope.kind != lifecycleGroup || m.lifecycle.scope.workspaceRoot != "/ws" || m.lifecycle.scope.group != "org" {
		t.Fatalf("group maintenance = %#v", m.lifecycle)
	}
}

func TestSheet_SLaunchesMain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{ID: "alpha", Name: "alpha", WorkspaceRoot: root, Path: path}
	m := newTestModel(p, nil)
	s := newProjectSheet(m, p, nil)
	m.sheet = s

	s.update(m, rune1('s'))

	if m.Launch == nil || m.Launch.Cwd != path {
		t.Errorf("Launch = %+v, want shell in %s", m.Launch, path)
	}
}

func TestSheet_WtDeleteRequiresConfirm(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-x", Branch: "wt/x"},
	}
	m := newTestModel(p, wts)
	s := newProjectSheet(m, p, nil)
	m.sheet = s

	// Move cursor to the non-main worktree row.
	wtVisIdx := -1
	for i, idx := range s.visible {
		if r := s.rows[idx]; r.kind == rowWorktree && r.wt != nil && !r.wt.IsMain {
			wtVisIdx = i
			break
		}
	}
	if wtVisIdx < 0 {
		t.Fatal("could not find non-main worktree row")
	}
	s.cursor = wtVisIdx

	s.update(m, rune1('d'))
	if s.pendingDel == nil {
		t.Fatalf("expected pending delete after 'd'")
	}
	// Cancel: any non-y key clears.
	s.update(m, rune1('n'))
	if s.pendingDel != nil {
		t.Errorf("non-y should cancel pending delete")
	}
}

func filterByKind(rows []sheetRow, k sheetRowKind) []sheetRow {
	var out []sheetRow
	for _, r := range rows {
		if r.kind == k {
			out = append(out, r)
		}
	}
	return out
}

func esc() tui.KeyMsg   { return tui.KeyMsg{Type: tui.KeyEsc} }
func enter() tui.KeyMsg { return tui.KeyMsg{Type: tui.KeyEnter} }
func rune1(r rune) tui.KeyMsg {
	return tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{r}}
}
