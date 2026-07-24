//go:build bench_l2

package benchfixture_test

import (
	"io"
	"log"
	"testing"

	"codeberg.org/kuchmenko/workspace/bench/benchfixture"
	workspacesync "codeberg.org/kuchmenko/workspace/internal/sync"
)

// BenchmarkSyncRun exercises one sync Runner run over a
// synthetic workspace: N projects, each with a real bare+worktree layout
// pointing at a fake file:// remote. The fake remotes are hot in
// page cache between iterations, so the dominant cost stays in Go +
// per-project git fetch syscalls (which is the realistic shape of a
// foreground sync on an idle machine).
//
// The workspace is not a git repo, so registry synchronization no-ops and
// the benchmark isolates project execution cost.
func BenchmarkSyncRunProjectsSmall(b *testing.B)  { benchSyncRun(b, 5) }
func BenchmarkSyncRunProjectsMedium(b *testing.B) { benchSyncRun(b, 25) }
func BenchmarkSyncRunProjectsLarge(b *testing.B)  { benchSyncRun(b, 100) }

func benchSyncRun(b *testing.B, projects int) {
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
	r := workspacesync.NewRunner(ws.Root, logger)

	// Warm the project remotes and conflict store.
	r.Run()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		r.Run()
	}
}
