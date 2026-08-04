package tui

import "testing"

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
