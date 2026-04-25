package branchprompt

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg constructs a tea.KeyMsg from a single-key string shortcut that
// the model accepts ("up", "down", "enter", "esc", "i", "k", "j").
// Text input in free-text mode uses KeyMsg with Runes populated.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// collectMsg runs cmd and returns the tea.Msg it produces, or nil.
// Mirrors how the bubbletea runtime dispatches the returned cmd back
// through Update — good enough for asserting the "one-shot message"
// emissions PickedMsg / CancelledMsg.
func collectMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestModel_PickFromCandidates_Enter(t *testing.T) {
	m := NewModel("acme-api", []string{"main", "master", "trunk"})

	// Arrow down once: cursor on "master".
	m, _ = m.Update(keyMsg("down"))
	if m.cursor != 1 {
		t.Fatalf("cursor: want 1, got %d", m.cursor)
	}

	// Enter picks it.
	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	msg := collectMsg(cmd)

	picked, ok := msg.(PickedMsg)
	if !ok {
		t.Fatalf("expected PickedMsg, got %T", msg)
	}
	if picked.Project != "acme-api" || picked.Branch != "master" {
		t.Errorf("picked = %+v", picked)
	}
}

func TestModel_CursorClamps(t *testing.T) {
	m := NewModel("x", []string{"main", "master"})

	// Up at 0 stays at 0.
	m, _ = m.Update(keyMsg("up"))
	if m.cursor != 0 {
		t.Errorf("cursor: want 0, got %d", m.cursor)
	}

	// Down past end stays at last.
	m, _ = m.Update(keyMsg("down"))
	m, _ = m.Update(keyMsg("down"))
	m, _ = m.Update(keyMsg("down"))
	if m.cursor != 1 {
		t.Errorf("cursor: want 1, got %d", m.cursor)
	}
}

func TestModel_EnterEmptyCandidates_FlipsToInputMode(t *testing.T) {
	m := NewModel("x", nil)

	m, _ = m.Update(keyMsg("enter"))
	if !m.inputMode {
		t.Fatal("expected inputMode after enter on empty candidates")
	}
}

func TestModel_IKey_FlipsToInputMode(t *testing.T) {
	m := NewModel("x", []string{"main"})

	m, _ = m.Update(keyMsg("i"))
	if !m.inputMode {
		t.Fatal("expected inputMode after 'i'")
	}
}

func TestModel_InputMode_EnterEmpty_NoEmit(t *testing.T) {
	m := NewModel("x", nil)
	m, _ = m.Update(keyMsg("enter"))
	// Now in input mode, empty value.
	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	if msg := collectMsg(cmd); msg != nil {
		t.Fatalf("expected no msg for empty enter, got %T", msg)
	}
	if !m.inputMode {
		t.Error("expected to stay in inputMode")
	}
}

func TestModel_InputMode_EscExits(t *testing.T) {
	m := NewModel("x", []string{"main"})
	m, _ = m.Update(keyMsg("i"))
	m, _ = m.Update(keyMsg("esc"))
	if m.inputMode {
		t.Fatal("expected to leave inputMode after esc")
	}
}

func TestModel_InputMode_EnterEmitsPicked(t *testing.T) {
	m := NewModel("demo", nil)
	m, _ = m.Update(keyMsg("enter")) // → inputMode
	// Simulate typing: feed the runes through the underlying textinput.
	// textinput.Model handles KeyRunes internally.
	m, _ = m.Update(keyMsg("develop"))

	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	msg := collectMsg(cmd)

	picked, ok := msg.(PickedMsg)
	if !ok {
		t.Fatalf("expected PickedMsg, got %T", msg)
	}
	if picked.Project != "demo" || picked.Branch != "develop" {
		t.Errorf("picked = %+v", picked)
	}
}

func TestModel_Escape_EmitsCancelled(t *testing.T) {
	m := NewModel("proj", []string{"main", "master"})
	var cmd tea.Cmd
	_, cmd = m.Update(keyMsg("esc"))
	msg := collectMsg(cmd)

	cancelled, ok := msg.(CancelledMsg)
	if !ok {
		t.Fatalf("expected CancelledMsg, got %T", msg)
	}
	if cancelled.Project != "proj" {
		t.Errorf("cancelled = %+v", cancelled)
	}
}

func TestModel_NonKeyMsg_NoOp(t *testing.T) {
	m := NewModel("x", []string{"main"})

	// Random non-key message type.
	type weirdMsg struct{}
	m2, cmd := m.Update(weirdMsg{})
	if cmd != nil {
		t.Error("expected nil cmd for non-key msg")
	}
	if m2.cursor != m.cursor || m2.inputMode != m.inputMode {
		t.Error("non-key msg mutated model")
	}
}

func TestModel_View_ContainsProjectAndCandidates(t *testing.T) {
	m := NewModel("acme", []string{"main", "trunk"})
	out := m.View()
	if !contains(out, "acme") {
		t.Error("View missing project name")
	}
	if !contains(out, "main") || !contains(out, "trunk") {
		t.Error("View missing candidates")
	}
}

func TestModel_View_EmptyCandidates_ShowsHint(t *testing.T) {
	m := NewModel("x", nil)
	out := m.View()
	if !contains(out, "No candidates") {
		t.Error("View missing empty-candidates hint")
	}
}

func TestModel_View_InputMode_ShowsInput(t *testing.T) {
	m := NewModel("x", nil)
	m, _ = m.Update(keyMsg("enter")) // → input mode
	out := m.View()
	if !contains(out, "Enter branch name") {
		t.Error("View missing input-mode hint")
	}
}

// contains is a tiny ANSI-tolerant substring check. View output is wrapped
// in lipgloss escapes, but the plain text we check for is always present.
func contains(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return i
		}
	}
	return -1
}
