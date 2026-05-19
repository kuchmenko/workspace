package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConfirmDialog_YesNoCancel(t *testing.T) {
	d := NewConfirmDialog(Amber, "Delete?")
	cases := []struct {
		key  tea.KeyMsg
		want Msg
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}, ConfirmedMsg{}},
		{tea.KeyMsg{Type: tea.KeyEnter}, ConfirmedMsg{}},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, CancelledMsg{}},
		{tea.KeyMsg{Type: tea.KeyEsc}, CancelledMsg{}},
	}
	for _, c := range cases {
		_, cmd := d.Update(c.key)
		if cmd == nil {
			t.Fatalf("no cmd for %v", c.key)
		}
		if got := cmd(); got != c.want {
			t.Errorf("for %v: got %T, want %T", c.key, got, c.want)
		}
	}
}
