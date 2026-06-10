package add

import (
	"codeberg.org/kuchmenko/workspace/internal/tui"
	"testing"
)

func keySpace() tui.KeyMsg { return tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{' '}} }

// browseModelWith returns a model that has already transitioned to
// browse with the given suggestions.
func browseModelWith(t *testing.T, items []Suggestion) AddModel {
	t.Helper()
	m := newTestModel(t, nil)
	m.sources = []Source{nil}
	m, _ = driveModel(m, sourceDoneMsg{name: "fake", items: items})
	if m.state != addStateBrowse {
		t.Fatalf("setup: state = %d, want browse", m.state)
	}
	return m
}

func TestBrowse_SpaceTogglesSelection(t *testing.T) {
	items := []Suggestion{
		{Name: "alpha", RemoteURL: "git@host:u/alpha.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
		{Name: "beta", RemoteURL: "git@host:u/beta.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)

	m, _ = driveModel(m, keySpace())
	if !m.selectedURLs["git@host:u/alpha.git"] {
		t.Errorf("space did not mark current row")
	}
	if len(m.selectedURLs) != 1 {
		t.Errorf("len = %d, want 1", len(m.selectedURLs))
	}

	// Toggle off.
	m, _ = driveModel(m, keySpace())
	if m.selectedURLs["git@host:u/alpha.git"] {
		t.Errorf("second space did not clear mark")
	}
}

func TestBrowse_AKeyToggleAllVisible(t *testing.T) {
	items := []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
		{Name: "b", RemoteURL: "g@h:u/b.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
		{Name: "c", RemoteURL: "g@h:u/c.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)

	m, _ = driveModel(m, keyRunes("a"))
	if len(m.selectedURLs) != 3 {
		t.Errorf("after first 'a': %d marked, want 3", len(m.selectedURLs))
	}
	// Second 'a' clears them.
	m, _ = driveModel(m, keyRunes("a"))
	if len(m.selectedURLs) != 0 {
		t.Errorf("after second 'a': %d marked, want 0", len(m.selectedURLs))
	}
}

func TestBrowse_EnterWithSelectionsTransitionsToBulkConfirm(t *testing.T) {
	items := []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
		{Name: "b", RemoteURL: "g@h:u/b.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)
	m, _ = driveModel(m, keySpace(), keyDown(), keySpace(), keyEnter())
	if m.state != addStateBulkConfirm {
		t.Errorf("state = %d, want bulkConfirm", m.state)
	}
}

func TestBrowse_EnterWithoutSelectionsKeepsSinglePath(t *testing.T) {
	items := []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)
	m, _ = driveModel(m, keyEnter())
	if m.state != addStateEdit {
		t.Errorf("state = %d, want edit", m.state)
	}
}

func TestBrowse_EscClearsSelectionBeforeQuitting(t *testing.T) {
	items := []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)
	m, _ = driveModel(m, keySpace())
	if len(m.selectedURLs) != 1 {
		t.Fatalf("setup: nothing marked")
	}
	// First esc clears.
	m, _ = driveModel(m, keyEsc())
	if len(m.selectedURLs) != 0 {
		t.Errorf("first esc did not clear marks: %d remain", len(m.selectedURLs))
	}
	if m.state != addStateBrowse {
		t.Errorf("first esc changed state away from browse: %d", m.state)
	}
}

func TestBuildBulkQueue_FiltersAlreadyRegistered(t *testing.T) {
	m := newTestModel(t, nil)
	m.allSuggestions = []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
		{Name: "b", RemoteURL: "g@h:u/b.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u", RegisteredPath: "personal/b"},
		{Name: "c", RemoteURL: "g@h:u/c.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m.selectedURLs = map[string]bool{
		"g@h:u/a.git": true,
		"g@h:u/b.git": true,
		"g@h:u/c.git": true,
	}
	queue := m.buildBulkQueue()
	if len(queue) != 2 {
		t.Fatalf("len = %d, want 2 (skipped already-registered)", len(queue))
	}
	for _, ef := range queue {
		if ef.URL == "g@h:u/b.git" {
			t.Errorf("registered URL leaked into queue")
		}
	}
}

func TestBuildBulkQueue_EmptyWhenNothingSelected(t *testing.T) {
	m := newTestModel(t, nil)
	m.allSuggestions = []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git"},
	}
	if got := m.buildBulkQueue(); len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestBulkConfirm_EscReturnsToBrowse(t *testing.T) {
	items := []Suggestion{
		{Name: "a", RemoteURL: "g@h:u/a.git", Sources: []SourceKind{SourceGitHub}, InferredGrp: "u"},
	}
	m := browseModelWith(t, items)
	m, _ = driveModel(m, keySpace(), keyEnter())
	if m.state != addStateBulkConfirm {
		t.Fatalf("setup: state = %d, want bulkConfirm", m.state)
	}
	m, _ = driveModel(m, keyEsc())
	if m.state != addStateBrowse {
		t.Errorf("state = %d, want browse", m.state)
	}
}
