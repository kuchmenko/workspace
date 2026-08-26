package agent

import (
	"testing"
	"time"
)

func TestHumanizeAgeAt(t *testing.T) {
	t0 := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{30 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{36 * time.Hour, "yday"},
		{3 * 24 * time.Hour, "3d"},
		{10 * 24 * time.Hour, "1w"},
		{60 * 24 * time.Hour, "2mo"},
		{800 * 24 * time.Hour, "2y"},
	}
	for _, tc := range cases {
		got := humanizeAgeAt(t0.Add(-tc.offset), t0)
		if got != tc.want {
			t.Errorf("humanizeAgeAt offset=%v: got %q, want %q", tc.offset, got, tc.want)
		}
	}
	if humanizeAge(time.Time{}) != "" {
		t.Errorf("zero time should produce empty string, not a humanized value")
	}
}
