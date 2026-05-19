package tui

import "testing"

func TestConfirmDialog_YesNoCancel(t *testing.T) {
	d := NewConfirmDialog(Amber, "Delete?")
	cases := []struct {
		key  KeyMsg
		want Msg
	}{
		{KeyMsg{Type: KeyRune, Runes: []rune{'y'}}, ConfirmedMsg{}},
		{KeyMsg{Type: KeyEnter}, ConfirmedMsg{}},
		{KeyMsg{Type: KeyRune, Runes: []rune{'n'}}, CancelledMsg{}},
		{KeyMsg{Type: KeyEsc}, CancelledMsg{}},
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
