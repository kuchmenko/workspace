package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func newTestModel(p *Project, wts []Worktree, sessByPath map[string][]Session) *Model {
	m := &Model{
		workspaces: []WorkspaceData{{
			Root:     "/ws",
			Projects: []Project{*p},
		}},
		expanded:  map[string]bool{},
		wtCache:   NewWorktreeCache(),
		sessCache: NewSessionCache(),
	}
	m.wtCache.data[p.Path] = wts
	for path, sessions := range sessByPath {
		m.sessCache.data[path] = sessions
	}
	// Sessions for paths not seeded must not trigger LoadSessions disk reads.
	if _, ok := m.sessCache.data[p.Path]; !ok {
		m.sessCache.data[p.Path] = nil
	}
	for i := range wts {
		if _, ok := m.sessCache.data[wts[i].Path]; !ok {
			m.sessCache.data[wts[i].Path] = nil
		}
	}
	return m
}

func TestBuildProjectSheetRows_ActionsAlwaysPresent(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil, nil)

	rows := buildProjectSheetRows(m, p, map[string]bool{})

	wantActions := []sheetAction{actClaudeMain, actShellMain, actPrompt, actNewWorktree, actSearch}
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
	m := newTestModel(p, wts, nil)

	rows := buildProjectSheetRows(m, p, map[string]bool{})

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

func TestBuildProjectSheetRows_SessionsNestedUnderWorktree(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-x", Branch: "wt/x"},
	}
	now := time.Now()
	sess := map[string][]Session{
		"/ws/alpha-wt-x": {{ID: "s1", Title: "rework", Cwd: "/ws/alpha-wt-x", Updated: now}},
	}
	m := newTestModel(p, wts, sess)

	// Closed: no session rows visible.
	rows := buildProjectSheetRows(m, p, map[string]bool{})
	if len(filterByKind(rows, rowSession)) != 0 {
		t.Errorf("collapsed worktree should not expose sessions")
	}

	// Open the worktree by its path key.
	rows = buildProjectSheetRows(m, p, map[string]bool{"/ws/alpha-wt-x": true})
	sessRows := filterByKind(rows, rowSession)
	if len(sessRows) != 1 || sessRows[0].session == nil || sessRows[0].session.ID != "s1" {
		t.Errorf("expected one session row for wt/x; got %+v", sessRows)
	}
}

func TestSheetFilter_KeepsSectionHeadersOnlyWhenSectionMatches(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-perf", Branch: "wt/perf-render"},
	}
	m := newTestModel(p, wts, nil)
	s := newProjectSheet(m, p, nil)

	s.filter = "perf"
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
		sessCache: NewSessionCache(),
	}
	m.sessCache.data["/ws/org"] = nil

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
	wantHead := []sheetAction{actClaudeMain, actShellMain, actPrompt, actSearch}
	for i, want := range wantHead {
		if actions[i] != want {
			t.Errorf("action[%d] = %v, want %v", i, actions[i], want)
		}
	}
}

func TestBuildGroupSheetRows_SessionsForGroupRoot(t *testing.T) {
	m := &Model{
		workspaces: []WorkspaceData{{
			Root:   "/ws",
			Groups: []string{"org"},
			Projects: []Project{
				{Name: "a", Group: "org"},
			},
		}},
		sessCache: NewSessionCache(),
	}
	now := time.Now()
	m.sessCache.data["/ws/org"] = []Session{
		{ID: "s1", Title: "hack at org root", Cwd: "/ws/org", Updated: now},
	}

	rows := buildGroupSheetRows(m, "org", "/ws/org")
	sess := filterByKind(rows, rowSession)
	if len(sess) != 1 || sess[0].session == nil || sess[0].session.ID != "s1" {
		t.Errorf("expected one session row from group root; got %+v", sess)
	}
}

func TestSheet_EscPopsToParent(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil, nil)
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

func TestSheet_EnterClaudeMainLaunches(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	m := newTestModel(p, nil, nil)
	s := newProjectSheet(m, p, nil)
	m.sheet = s

	// Cursor starts at row 0 = claude in main.
	s.update(m, enter())

	if m.Launch == nil || m.Launch.Cwd != "/ws/alpha" || m.Launch.ShellOnly {
		t.Errorf("Launch = %+v, want claude in /ws/alpha", m.Launch)
	}
}

func TestSheet_WtDeleteRequiresConfirm(t *testing.T) {
	p := &Project{ID: "alpha", Name: "alpha", Path: "/ws/alpha"}
	wts := []Worktree{
		{Path: "/ws/alpha", IsMain: true},
		{Path: "/ws/alpha-wt-x", Branch: "wt/x"},
	}
	m := newTestModel(p, wts, nil)
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
