package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWindowAround(t *testing.T) {
	cases := []struct {
		name         string
		cursor       int
		total        int
		size         int
		wantS, wantE int
	}{
		{"empty", 0, 0, 16, 0, 0},
		{"nonpositive size", 0, 10, 0, 0, 0},
		{"total fits", 5, 10, 16, 0, 10},
		{"near start", 2, 100, 16, 0, 16},
		{"middle", 50, 100, 16, 42, 58},
		{"near end", 98, 100, 16, 84, 100},
		{"negative cursor", -1, 100, 16, 0, 16},
		{"cursor past end", 100, 100, 16, 84, 100},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			start, end := WindowAround(test.cursor, test.total, test.size)
			if start != test.wantS || end != test.wantE {
				t.Fatalf("got (%d, %d), want (%d, %d)", start, end, test.wantS, test.wantE)
			}
		})
	}
}

func TestTruncatePreservesANSIAndWideUnicodeWidth(t *testing.T) {
	got := Truncate("\x1b[31m界界界\x1b[0m", 5)
	if Width(got) != 5 {
		t.Fatalf("width = %d, want 5: %q", Width(got), got)
	}
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "…") {
		t.Fatalf("ANSI or truncation marker missing: %q", got)
	}
}

func TestPlaceClipsContentToTerminalBounds(t *testing.T) {
	for _, size := range []struct{ width, height int }{{1, 1}, {10, 5}, {24, 8}} {
		content := strings.Repeat("界", 20) + "\n" + strings.Repeat("line\n", 20)
		got := Place(size.width, size.height, Center, Center, content)
		if Height(got) > size.height {
			t.Fatalf("Place(%d, %d) height = %d", size.width, size.height, Height(got))
		}
		for _, line := range strings.Split(got, "\n") {
			if Width(line) > size.width {
				t.Fatalf("Place(%d, %d) line width = %d: %q", size.width, size.height, Width(line), line)
			}
		}
	}
}

func TestControlKeyConversions(t *testing.T) {
	tests := []struct {
		own  KeyType
		tea  tea.KeyType
		name string
	}{
		{KeyCtrlU, tea.KeyCtrlU, "ctrl+u"},
		{KeyCtrlF, tea.KeyCtrlF, "ctrl+f"},
		{KeyCtrlB, tea.KeyCtrlB, "ctrl+b"},
		{KeyCtrlO, tea.KeyCtrlO, "ctrl+o"},
		{KeyCtrlQ, tea.KeyCtrlQ, "ctrl+q"},
		{KeyCtrlS, tea.KeyCtrlS, "ctrl+s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fromTea := keyMsgFromBubbletea(tea.KeyMsg{Type: test.tea})
			if fromTea.Type != test.own || !fromTea.Ctrl || fromTea.String() != test.name {
				t.Fatalf("from Bubble Tea = %#v (%q)", fromTea, fromTea.String())
			}
			toTea := keyMsgToBubbletea(KeyMsg{Type: test.own, Ctrl: true})
			if toTea.Type != test.tea {
				t.Fatalf("to Bubble Tea = %v, want %v", toTea.Type, test.tea)
			}
		})
	}
}
