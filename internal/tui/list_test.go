package tui

import (
	"strings"
	"testing"
)

type strItem string

func (s strItem) Title() string       { return string(s) }
func (s strItem) FilterValue() string { return string(s) }

func key(s string) KeyMsg {
	switch s {
	case "esc":
		return KeyMsg{Type: KeyEsc}
	case "enter":
		return KeyMsg{Type: KeyEnter}
	case "backspace":
		return KeyMsg{Type: KeyBackspace}
	case "down":
		return KeyMsg{Type: KeyDown}
	case "up":
		return KeyMsg{Type: KeyUp}
	case "tab":
		return KeyMsg{Type: KeyTab}
	}
	return KeyMsg{Type: KeyRune, Runes: []rune(s)}
}

func TestFilterableList_CursorMovement(t *testing.T) {
	l := NewFilterableList(Amber, []ListItem{strItem("a"), strItem("b"), strItem("c")})
	if got := l.Cursor(); got != 0 {
		t.Fatalf("initial cursor = %d, want 0", got)
	}
	m, _ := l.Update(key("j"))
	l = m.(FilterableList)
	if got := l.Cursor(); got != 1 {
		t.Errorf("after j: cursor = %d, want 1", got)
	}
	m, _ = l.Update(key("G"))
	l = m.(FilterableList)
	if got := l.Cursor(); got != 2 {
		t.Errorf("after G: cursor = %d, want 2", got)
	}
}

func TestFilterableList_FilterMode(t *testing.T) {
	l := NewFilterableList(Amber, []ListItem{strItem("apple"), strItem("banana"), strItem("avocado")})
	m, _ := l.Update(key("/"))
	l = m.(FilterableList)
	if !l.FilterMode() {
		t.Fatal("expected filter mode active after /")
	}
	for _, c := range "av" {
		m, _ = l.Update(KeyMsg{Type: KeyRune, Runes: []rune{c}})
		l = m.(FilterableList)
	}
	if l.Filter() != "av" {
		t.Errorf("filter = %q, want %q", l.Filter(), "av")
	}
	sel, ok := l.Selected()
	if !ok {
		t.Fatal("expected selection after filter")
	}
	if !strings.Contains(sel.Title(), "avocado") {
		t.Errorf("first match = %q, want avocado", sel.Title())
	}
}

func TestFilterableList_EmptyView(t *testing.T) {
	l := NewFilterableList(Amber, nil)
	v := l.View()
	if !strings.Contains(v, "empty") {
		t.Errorf("empty list view = %q", v)
	}
}
