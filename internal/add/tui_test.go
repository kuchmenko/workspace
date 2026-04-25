package add

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// driveModel feeds a sequence of tea.Msg through Update and returns
// the final model + the list of tea.Cmd outputs. This is enough to
// test state transitions without the full bubbletea runtime.
func driveModel(m AddModel, msgs ...tea.Msg) (AddModel, []tea.Cmd) {
	var cmds []tea.Cmd
	for _, msg := range msgs {
		// Bypass debounce by clearing stateChangedAt — the
		// production debounce is real-time-based, tests want
		// deterministic stepping.
		m.stateChangedAt = time.Time{}
		mm, cmd := m.Update(msg)
		m = mm.(AddModel)
		cmds = append(cmds, cmd)
	}
	return m, cmds
}

// runCmd runs a tea.Cmd and returns the message it produces, or nil.
func runCmd(c tea.Cmd) tea.Msg {
	if c == nil {
		return nil
	}
	return c()
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyEnter() tea.KeyMsg     { return tea.KeyMsg{Type: tea.KeyEnter} }
func keyEsc() tea.KeyMsg       { return tea.KeyMsg{Type: tea.KeyEscape} }
func keyDown() tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyDown} }
func keyTab() tea.KeyMsg       { return tea.KeyMsg{Type: tea.KeyTab} }
func keyBackspace() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyBackspace} }

func newTestModel(t *testing.T, sources []Source) AddModel {
	t.Helper()
	wsRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ws := &config.Workspace{
		Projects: map[string]config.Project{},
	}
	return NewAddModel(AddModelOptions{
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      func(*config.Workspace) error { return nil },
		Sources:   sources,
	})
}

// staticSource always returns the same suggestions.
type staticSource struct {
	name  string
	items []Suggestion
}

func (s *staticSource) Name() string { return s.name }
func (s *staticSource) FetchSuggestions(_ ctxLike) ([]Suggestion, error) {
	return s.items, nil
}

// ctxLike is the subset of context.Context that fakeSource needs —
// duplicated here so the test file doesn't need to import context for
// the few faux sources.
type ctxLike interface {
	Done() <-chan struct{}
	Err() error
	Deadline() (time.Time, bool)
	Value(key any) any
}

func TestAddModel_GatherDone_NonEmpty_TransitionsToBrowse(t *testing.T) {
	m := newTestModel(t, nil)
	// One source returning two suggestions — model must transition
	// to browse and accumulate the items.
	m.sources = []Source{nil} // count matters for sourcesDone math
	m, _ = driveModel(m, sourceDoneMsg{
		name: "fake",
		items: []Suggestion{
			{Name: "alpha", RemoteURL: "git@github.com:me/alpha.git", Sources: []SourceKind{SourceGitHub}},
			{Name: "beta", RemoteURL: "git@github.com:me/beta.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/beta"},
		},
	})
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want addStateBrowse", m.state)
	}
	if len(m.allSuggestions) != 2 {
		t.Errorf("suggestions: %d", len(m.allSuggestions))
	}
}

func TestAddModel_GatherDone_Empty_TransitionsToBrowseEmpty(t *testing.T) {
	m := newTestModel(t, nil)
	// Single source that returns no items: model must end up in
	// browseEmpty after all sources finish.
	m.sources = []Source{nil}
	m, _ = driveModel(m, sourceDoneMsg{name: "fake", items: nil})
	if m.state != addStateBrowseEmpty {
		t.Errorf("state = %d, want addStateBrowseEmpty", m.state)
	}
}

func TestAddModel_StreamingGather_FirstResultTransitionsImmediately(t *testing.T) {
	// With three sources, the model should transition to browse the
	// moment any one returns non-empty results — before the others
	// finish. Subsequent source completions fold in without changing
	// state.
	m := newTestModel(t, nil)
	m.sources = []Source{nil, nil, nil}

	// First source: 1 item → transition to browse.
	m, _ = driveModel(m, sourceDoneMsg{name: "disk", items: []Suggestion{
		{Name: "a", RemoteURL: "g@h:me/a.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/a"},
	}})
	if m.state != addStateBrowse {
		t.Errorf("after source 1: state = %d, want browse", m.state)
	}
	if len(m.allSuggestions) != 1 {
		t.Errorf("after source 1: suggestions = %d", len(m.allSuggestions))
	}

	// Second source: empty → no state change, count stays.
	m, _ = driveModel(m, sourceDoneMsg{name: "clip"})
	if m.state != addStateBrowse {
		t.Errorf("after source 2: state changed, got %d", m.state)
	}
	if len(m.allSuggestions) != 1 {
		t.Errorf("empty source 2 mutated suggestions: %d", len(m.allSuggestions))
	}

	// Third source: 2 more items → suggestions grow.
	m, _ = driveModel(m, sourceDoneMsg{name: "github", items: []Suggestion{
		{Name: "b", RemoteURL: "g@h:me/b.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "me"},
		{Name: "c", RemoteURL: "g@h:me/c.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "me"},
	}})
	if m.state != addStateBrowse {
		t.Errorf("after source 3: state = %d, want browse", m.state)
	}
	if len(m.allSuggestions) != 3 {
		t.Errorf("after all sources: suggestions = %d, want 3", len(m.allSuggestions))
	}
	if m.sourcesDone != 3 {
		t.Errorf("sourcesDone = %d, want 3", m.sourcesDone)
	}
	if len(m.sourceOutcomes) != 3 {
		t.Errorf("sourceOutcomes = %d, want 3", len(m.sourceOutcomes))
	}
}

func TestAddModel_StreamingGather_SourceErrIsRecorded(t *testing.T) {
	m := newTestModel(t, nil)
	m.sources = []Source{nil, nil}
	// Source 1 errors → no items, error in outcomes.
	m, _ = driveModel(m, sourceDoneMsg{name: "github", err: testutilFailErr("401")})
	if len(m.sourceOutcomes) != 1 || m.sourceOutcomes[0].Err == nil {
		t.Errorf("err not recorded: %+v", m.sourceOutcomes)
	}
	// Source 2 succeeds → transition to browse.
	m, _ = driveModel(m, sourceDoneMsg{name: "disk", items: []Suggestion{{Name: "x"}}})
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want browse", m.state)
	}
}

func TestAddModel_StreamingGather_AllErrorsAndEmpty_BrowseEmpty(t *testing.T) {
	m := newTestModel(t, nil)
	m.sources = []Source{nil, nil}
	// Both sources fail with no items. Model must end up in
	// browseEmpty so the user sees the "no suggestions" hint with
	// the error chips visible above.
	m, _ = driveModel(m, sourceDoneMsg{name: "a", err: testutilFailErr("a-err")})
	m, _ = driveModel(m, sourceDoneMsg{name: "b", err: testutilFailErr("b-err")})
	if m.state != addStateBrowseEmpty {
		t.Errorf("state = %d, want browseEmpty", m.state)
	}
}

func TestAddModel_Browse_ArrowKeys(t *testing.T) {
	m := newTestModel(t, nil)
	m.allSuggestions = []Suggestion{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m.state = addStateBrowse

	m, _ = driveModel(m, keyDown(), keyDown())
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	m, _ = driveModel(m, keyDown()) // clamped at last
	if m.cursor != 2 {
		t.Errorf("cursor clamp: %d", m.cursor)
	}
}

func TestAddModel_Browse_IKey_OpensManual(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateBrowse
	m, _ = driveModel(m, keyRunes("i"))
	if m.state != addStateManual {
		t.Errorf("state = %d, want addStateManual", m.state)
	}
}

func TestAddModel_Browse_Filter_NarrowsView(t *testing.T) {
	m := newTestModel(t, nil)
	m.allSuggestions = []Suggestion{
		{Name: "alpha", RemoteURL: "git@github.com:me/alpha.git"},
		{Name: "beta", RemoteURL: "git@github.com:me/beta.git"},
		{Name: "alphabet", RemoteURL: "git@github.com:me/alphabet.git"},
	}
	m.state = addStateBrowse

	// `/` enables filter mode.
	m, _ = driveModel(m, keyRunes("/"))
	if !m.filterMode {
		t.Fatal("expected filterMode after /")
	}
	// Type "alpha".
	for _, r := range []rune("alpha") {
		m, _ = driveModel(m, keyRunes(string(r)))
	}
	view := m.filteredView()
	if len(view) != 2 {
		t.Errorf("filtered view: got %d, want 2 (alpha, alphabet)", len(view))
	}
	// Enter commits filter, exit filter mode.
	m, _ = driveModel(m, keyEnter())
	if m.filterMode {
		t.Error("expected filterMode false after enter")
	}
	if m.cursor != 0 {
		t.Errorf("cursor reset: %d", m.cursor)
	}
}

func TestAddModel_Browse_EnterTransitionsToEdit(t *testing.T) {
	m := newTestModel(t, nil)
	m.allSuggestions = []Suggestion{
		{Name: "alpha", RemoteURL: "git@github.com:me/alpha.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "me"},
	}
	m.state = addStateBrowse
	m.cursor = 0

	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Fatalf("state = %d, want addStateEdit", m.state)
	}
	if m.editFields.Name != "alpha" {
		t.Errorf("editFields.Name = %q", m.editFields.Name)
	}
	if m.editFields.URL != "git@github.com:me/alpha.git" {
		t.Errorf("editFields.URL = %q", m.editFields.URL)
	}
}

func TestAddModel_Browse_EscQuitsWithEmptyResult(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateBrowse
	m.allSuggestions = []Suggestion{{Name: "x"}}
	m.standalone = true
	mm, cmd := m.Update(keyEsc())
	m = mm.(AddModel)
	if m.state != addStateDone {
		t.Errorf("state after esc: %d", m.state)
	}
	// Should have emitted AddDoneMsg + tea.Quit.
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
}

func TestAddModel_Manual_EmptyURL_ShowsError(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateManual
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateManual {
		t.Errorf("state should stay manual on empty URL, got %d", m.state)
	}
	if m.manualErr == "" {
		t.Error("expected manualErr to be set")
	}
}

func TestAddModel_Manual_ValidURL_TransitionsToEdit(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateManual
	m.manualInput.SetValue("git@github.com:foo/bar.git")
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Fatalf("state = %d, want addStateEdit", m.state)
	}
	if m.editFields.URL != "git@github.com:foo/bar.git" {
		t.Errorf("URL = %q", m.editFields.URL)
	}
	if m.editFields.Name != "bar" {
		t.Errorf("Name = %q, want bar", m.editFields.Name)
	}
}

func TestAddModel_Manual_EscReturnsToBrowse(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateManual
	m, _ = driveModel(m, keyEsc())
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want addStateBrowse", m.state)
	}
}

func TestAddModel_Edit_Validates_NameRequired(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateEdit
	m.editFields = editFields{URL: "git@github.com:foo/bar.git", Category: config.CategoryPersonal}
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Errorf("state should stay edit, got %d", m.state)
	}
	if m.editErr == "" {
		t.Error("expected editErr")
	}
}

func TestAddModel_Edit_NameConflict_ShowsError(t *testing.T) {
	m := newTestModel(t, nil)
	m.ws.Projects["dup"] = config.Project{Path: "personal/dup"}
	m.state = addStateEdit
	m.editFields = editFields{Name: "dup", URL: "git@github.com:foo/dup.git", Category: config.CategoryPersonal}
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Error("state should stay edit on name conflict")
	}
	if m.editErr == "" || m.editErr == " " {
		t.Errorf("expected name-conflict error, got %q", m.editErr)
	}
}

func TestAddModel_Edit_TabCyclesFocus(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateEdit
	for i := 0; i < 4; i++ {
		focusBefore := m.editFocus
		m, _ = driveModel(m, keyTab())
		if m.editFocus != (focusBefore+1)%4 {
			t.Errorf("tab i=%d: focus %d → %d (want +1)", i, focusBefore, m.editFocus)
		}
	}
}

func TestAddModel_Edit_TypingNameUpdatesPath(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateEdit
	m.editFields = editFields{
		URL:      "git@github.com:foo/bar.git",
		Category: config.CategoryPersonal,
	}
	m.editFocus = 0 // Name
	for _, r := range []rune("acme") {
		m, _ = driveModel(m, keyRunes(string(r)))
	}
	if m.editFields.Name != "acme" {
		t.Errorf("Name = %q", m.editFields.Name)
	}
	if m.editFields.Path != filepath.Join("personal", "acme") {
		t.Errorf("Path = %q", m.editFields.Path)
	}
}

func TestAddModel_Edit_EscReturnsToBrowse(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateEdit
	m, _ = driveModel(m, keyEsc())
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want addStateBrowse", m.state)
	}
}

func TestAddModel_Edit_BackspaceClipsName(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateEdit
	m.editFields = editFields{Name: "abc"}
	m.editFocus = 0
	m, _ = driveModel(m, keyBackspace())
	if m.editFields.Name != "ab" {
		t.Errorf("Name = %q", m.editFields.Name)
	}
}

func TestAddModel_Confirm_YEnqueuesAndStartsCloning(t *testing.T) {
	m := newTestModel(t, nil)
	m.editFields = editFields{
		Name: "x", URL: "g@h:a/x.git", Category: config.CategoryPersonal, Path: "personal/x",
	}
	m.state = addStateConfirm
	m, _ = driveModel(m, keyRunes("y"))
	if m.state != addStateCloning {
		t.Errorf("state = %d, want addStateCloning", m.state)
	}
	if len(m.queue) != 1 {
		t.Errorf("queue: %d", len(m.queue))
	}
	if m.queue[0].Name != "x" {
		t.Errorf("queue[0].Name = %q", m.queue[0].Name)
	}
}

func TestAddModel_Confirm_NReturnsToBrowse(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateConfirm
	m, _ = driveModel(m, keyRunes("n"))
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want addStateBrowse", m.state)
	}
}

func TestAddModel_Cloning_CloneDoneSuccess(t *testing.T) {
	m := newTestModel(t, nil)
	m.queue = []editFields{{Name: "a"}}
	m.state = addStateCloning
	m.standalone = true
	proj := config.Project{Remote: "g@h:a/a.git", Path: "personal/a"}
	mm, cmd := m.Update(cloneDoneMsg{idx: 0, project: proj})
	m = mm.(AddModel)
	if len(m.added) != 1 {
		t.Errorf("added: %d", len(m.added))
	}
	if m.state != addStateDone {
		t.Errorf("state should advance to done after queue empty, got %d", m.state)
	}
	if cmd == nil {
		t.Error("expected emit + Quit cmd")
	}
}

func TestAddModel_Cloning_CloneDoneSkipped(t *testing.T) {
	m := newTestModel(t, nil)
	m.queue = []editFields{{Name: "a"}, {Name: "b"}}
	m.state = addStateCloning

	mm, _ := m.Update(cloneDoneMsg{idx: 0, skipped: &SkipReason{URL: "g@h:a/a.git", Reason: "registered"}})
	m = mm.(AddModel)
	if len(m.skipped) != 1 {
		t.Errorf("skipped: %d", len(m.skipped))
	}
	if m.currentIdx != 1 {
		t.Errorf("currentIdx: %d", m.currentIdx)
	}
}

func TestAddModel_Cloning_CloneDoneError(t *testing.T) {
	m := newTestModel(t, nil)
	m.queue = []editFields{{Name: "a"}, {Name: "b"}}
	m.state = addStateCloning

	mm, _ := m.Update(cloneDoneMsg{idx: 0, err: testutilFailErr("fail")})
	m = mm.(AddModel)
	if len(m.errors) != 1 {
		t.Errorf("errors: %d", len(m.errors))
	}
}

// testutilFailErr returns an error fixture for cloneDoneMsg tests.
type testFailErr struct{ msg string }

func (e *testFailErr) Error() string                                  { return e.msg }
func testutilFailErr(s string) error                                  { return &testFailErr{msg: s} }

func TestAddModel_Done_AnyKeyQuits_Standalone(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateDone
	m.standalone = true
	_, cmd := m.Update(keyEnter())
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestAddModel_Done_AnyKeyNoOp_Embedded(t *testing.T) {
	m := newTestModel(t, nil)
	m.state = addStateDone
	m.standalone = false
	_, cmd := m.Update(keyEnter())
	if cmd != nil {
		t.Error("embedded mode: done state should not auto-quit")
	}
}

func TestAddModel_CtrlC_AlwaysGoesToDone(t *testing.T) {
	for _, st := range []addState{addStateGathering, addStateBrowse, addStateEdit, addStateConfirm} {
		m := newTestModel(t, nil)
		m.state = st
		m.standalone = true
		ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
		mm, cmd := m.Update(ctrlC)
		m = mm.(AddModel)
		if m.state != addStateDone {
			t.Errorf("state %d → ctrl+c → got %d, want done", st, m.state)
		}
		if cmd == nil {
			t.Errorf("state %d → ctrl+c: nil cmd", st)
		}
	}
}

// IntegrationTest: drive a complete flow from gather → browse → edit
// → confirm → cloning. Uses a real fake remote so Register actually
// clones and writes workspace.toml.
func TestAddModel_FullHappyPath(t *testing.T) {
	wsRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ws := &config.Workspace{Projects: map[string]config.Project{}}
	saved := false
	saveFn := func(*config.Workspace) error { saved = true; return nil }

	url := testutil.InitFakeRemote(t, "happy", "main")
	suggestion := Suggestion{
		Name:      "happy",
		RemoteURL: url,
		Sources:   []SourceKind{SourceGitHub},
	}

	m := NewAddModel(AddModelOptions{
		WsRoot:    wsRoot,
		Workspace: ws,
		Save:      saveFn,
		Sources:   nil,
	})
	m.standalone = true
	m.sources = []Source{nil} // sourcesDone math expects a non-empty count

	// 1. Pretend a single source returned our suggestion.
	m, _ = driveModel(m, sourceDoneMsg{name: "fake", items: []Suggestion{suggestion}})
	if m.state != addStateBrowse {
		t.Fatalf("state after gather: %d", m.state)
	}

	// 2. Hit enter on the first suggestion → edit.
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Fatalf("state after enter: %d", m.state)
	}

	// 3. Hit enter on edit (validation should pass — Name and URL set
	//    from the suggestion).
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateConfirm {
		t.Fatalf("state after edit-enter: %d (err %q)", m.state, m.editErr)
	}

	// 4. Hit 'y' on confirm → cloning, queue has one entry, cmd
	//    starts the clone.
	var cmds []tea.Cmd
	m, cmds = driveModel(m, keyRunes("y"))
	if m.state != addStateCloning {
		t.Fatalf("state after confirm: %d", m.state)
	}
	if len(m.queue) != 1 {
		t.Fatalf("queue: %d", len(m.queue))
	}

	// 5. Run the clone cmd to completion. It returns cloneDoneMsg
	//    that we feed back into Update.
	cdone := runCmd(cmds[len(cmds)-1])
	// cmd is tea.Batch(spinnerTick, startCloneJob); we need only
	// the latter. Drive both — spinner.Tick produces a TickMsg that
	// updateCloning happily ignores, then startCloneJob produces
	// cloneDoneMsg.
	if cd, ok := cdone.(cloneDoneMsg); ok {
		m, _ = driveModel(m, cd)
	} else {
		// tea.Batch wraps multiple cmds; run startCloneJob directly
		// to keep the test deterministic. Either path lands the
		// same cloneDoneMsg.
		direct := m.startCloneJob(0)()
		if cd, ok := direct.(cloneDoneMsg); ok {
			m, _ = driveModel(m, cd)
		}
	}

	// 6. Assert outcome: project added, save fired, state done.
	if len(m.added) != 1 {
		t.Errorf("added: %d, want 1", len(m.added))
	}
	if !saved {
		t.Error("Save not called")
	}
	if m.state != addStateDone {
		t.Errorf("final state: %d, want done", m.state)
	}
	if _, ok := ws.Projects["happy"]; !ok {
		t.Error("workspace.Projects missing 'happy'")
	}
}

