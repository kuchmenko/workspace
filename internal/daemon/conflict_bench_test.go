package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/daemon"
)

// BenchmarkRecord_Fresh measures the cost of inserting a brand-new
// conflict into an empty store. Hits the JSON encode + tmp-rename path.
// Each iteration starts with a fresh store so we don't drift into
// "linear-scan-amortized" territory.
//
// One parent tempdir + one Store are allocated up front; per-iteration
// "freshness" is achieved by deleting the on-disk JSON file before the
// timer restarts. The earlier shape called `storeAt(b)` in the loop,
// which routed through `b.TempDir()` and registered b.N (~25k+) cleanup
// entries on the testing.B — exhausting tmp-quota machines at default
// gate count.
func BenchmarkRecord_Fresh(b *testing.B) {
	b.ReportAllocs()

	dir := b.TempDir()
	b.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	s, err := daemon.OpenConflictStore()
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	confPath, err := daemon.ConflictPath()
	if err != nil {
		b.Fatalf("resolve path: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Empty store == file absent. Record() recreates it on first
		// write. ENOENT on a never-yet-written iter is silent.
		_ = os.Remove(confPath)
		b.StartTimer()

		_, err := s.Record(daemon.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "main",
			Kind:      daemon.KindNeedsBootstrap,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRecord_DuplicateMatch measures the hot path the reconciler
// hits on every tick: same conflict already in store, just refresh
// DetectedAt. Linear scan over existing entries dominates.
func BenchmarkRecord_DuplicateMatch_10(b *testing.B)  { benchDuplicateMatch(b, 10) }
func BenchmarkRecord_DuplicateMatch_100(b *testing.B) { benchDuplicateMatch(b, 100) }

func benchDuplicateMatch(b *testing.B, existing int) {
	b.Helper()
	s := storeAt(b)
	for i := 0; i < existing; i++ {
		if _, err := s.Record(daemon.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "main",
			Kind:      daemon.KindNeedsBootstrap,
		}); err != nil {
			b.Fatal(err)
		}
	}
	target := daemon.Conflict{
		Workspace:  "/ws",
		Project:    fmt.Sprintf("proj-%d", existing-1), // last entry — worst case for linear scan
		Branch:     "main",
		Kind:       daemon.KindNeedsBootstrap,
		DetectedAt: time.Now().UTC(),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := s.Record(target); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkList measures the cost of decoding the persisted JSON. Hot
// when CLI commands query the store for display.
func BenchmarkList_10(b *testing.B)  { benchList(b, 10) }
func BenchmarkList_100(b *testing.B) { benchList(b, 100) }

func benchList(b *testing.B, count int) {
	b.Helper()
	s := storeAt(b)
	for i := 0; i < count; i++ {
		if _, err := s.Record(daemon.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "feat/branch",
			Kind:      daemon.KindBranchOrphan,
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := s.List(); err != nil {
			b.Fatal(err)
		}
	}
}

// storeAt creates a fresh Store backed by t.TempDir(). Sets XDG_STATE_HOME
// before Open so we never touch the real ~/.local/state/ws/conflicts.json
// — see feedback_test_xdg_isolation.md.
func storeAt(b *testing.B) *daemon.ConflictStore {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	s, err := daemon.OpenConflictStore()
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	return s
}
