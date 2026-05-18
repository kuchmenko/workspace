package daemon_test

import (
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/daemon"
)

func TestResolvedPushCooldown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		// Empty → default. The common case: a workspace entry written before
		// push_cooldown existed must get the new coalescing behavior for free.
		{"unset", "", daemon.DefaultPushCooldown},

		// Literal "0" disables coalescing. time.ParseDuration rejects a bare
		// "0" (no unit) so this is the dedicated short-circuit — the
		// regression that motivated this test.
		{"bare zero", "0", 0},
		{"zero seconds", "0s", 0},

		{"valid", "30m", 30 * time.Minute},
		{"valid hours", "2h", 2 * time.Hour},

		// Garbage falls back to the default rather than silently dropping
		// the daemon to no-coalesce. Surprises here would be hard to debug
		// (the symptom is just "history is noisy again").
		{"garbage", "not-a-duration", daemon.DefaultPushCooldown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := daemon.WorkspaceEntry{PushCooldown: tc.in}.ResolvedPushCooldown()
			if got != tc.want {
				t.Fatalf("ResolvedPushCooldown(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
