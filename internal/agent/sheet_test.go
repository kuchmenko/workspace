package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func newTestModel(p *Project, wts []Worktree) *Model {
	m := &Model{
		workspaces: []WorkspaceData{{Root: "/ws", Projects: []Project{*p}}},
		expanded:   map[string]bool{},
		wtCache:    NewWorktreeCache(),
	}
	m.wtCache.data[p.Path] = wts
	return m
}

func TestBuildProjectSheetRows_ActionsAlwaysPresent(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil)

	rows := buildProjectSheetRows(m, p)

	wantActions := []sheetAction{actShellMain, actNewWorktree, actSearch}
	got := actionSeq(rows)[:len(wantActions)]
	for i, want := range wantActions {
		if got[i] != want {
			t.Errorf("action[%d] = %v, want %v", i, got[i], want)
		}
	}

	// edit + favorite always at the tail under "manage".
	tail := actionSeq(rows)
	if len(tail) < 2 || tail[len(tail)-2] != actEdit || tail[len(tail)-1] != actFavorite {
		t.Errorf("manage actions missing or out of order: %v", tail)
	}
}

func TestBuildProjectSheetRows_WorktreesSection(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-x", Branch: "wt/x"},
		{Path: "/ws/alpha-wt-y", Branch: "wt/y", Dirty: true, Ahead: 3},
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
		switch r.kind {
		case rowHeader:
			if !strings.Contains(r.label, "worktrees") {
				t.Errorf("unexpected surviving header: %q", r.label)
			}
		case rowAction:
			t.Errorf("action row %q should be filtered out by 'perf'", r.label)
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

	rows := buildGroupSheetRows(m, "org", "/ws/org")

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

	actions := actionSeq(rows)
	wantHead := []sheetAction{actShellMain, actSearch}
	for i, want := range wantHead {
		if actions[i] != want {
			t.Errorf("action[%d] = %v, want %v", i, actions[i], want)
		}
	}
}

func TestSheet_EscPopsToParent(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil)
	parent := newGroupSheet(m, "org")
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

func TestSheet_EnterShellMainLaunches(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil)
	s := newProjectSheet(m, p, nil)
	m.sheet = s

	// Cursor starts at row 0 = shell in main.
	s.update(m, enter())

	if m.Launch == nil || m.Launch.Cwd != "/ws/alpha" {
		t.Errorf("Launch = %+v, want shell in /ws/alpha", m.Launch)
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

// ---------- helpers ----------

func actionSeq(rows []sheetRow) []sheetAction {
	var out []sheetAction
	for _, r := range rows {
		if r.kind == rowAction {
			out = append(out, r.action)
		}
	}
	return out
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
