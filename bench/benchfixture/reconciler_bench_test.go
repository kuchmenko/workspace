//go:build bench_l2

package benchfixture_test

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/bench/benchfixture"
	"github.com/kuchmenko/workspace/internal/daemon"
)

// BenchmarkReconcilerTick exercises a single reconciler.Tick over a
// synthetic workspace: N projects, each with a real bare+worktree layout
// pointing at a fake file:// remote. The fake remotes are hot in
// page cache between iterations, so the dominant cost stays in Go +
// per-project git fetch syscalls (which is the realistic shape of a
// daemon tick on an idle machine).
//
// Workspace is NOT a git repo here, so Phase 1 (syncTOML) no-ops and we
// isolate Phase 2 cost. A separate benchmark could wrap the workspace in
// `git init` to measure Phase 1 overhead.
func BenchmarkReconcilerTickPhase2_Small(b *testing.B)  { benchReconcilerTick(b, 5) }
func BenchmarkReconcilerTickPhase2_Medium(b *testing.B) { benchReconcilerTick(b, 25) }
func BenchmarkReconcilerTickPhase2_Large(b *testing.B)  { benchReconcilerTick(b, 100) }

func benchReconcilerTick(b *testing.B, projects int) {
	b.Helper()
	// Isolate XDG so the conflict store doesn't leak into the user's real
	// state — see feedback_test_xdg_isolation.md.
	b.Setenv("XDG_STATE_HOME", b.TempDir())
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	b.Setenv("HOME", b.TempDir())

	ws := benchfixture.Build(b, benchfixture.Options{
		Projects: projects,
		Cloned:   true,
	})

	logger := log.New(io.Discard, "", 0)
	r := daemon.NewReconciler(ws.Root, time.Hour, logger)
	r.SetAutoBootstrap(false) // synthetic remotes are tiny, no auto-action needed

	// Warm: first tick populates internal state (fetches, conflict store).
	r.Tick()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		r.Tick()
	}
}
