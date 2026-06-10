//go:build bench_l2

package benchfixture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/bench/benchfixture"
)

// BenchmarkScanWalk exercises the directory walk that powers `ws scan`,
// without forking real `git rev-parse` (which would dwarf the Go cost).
// We synthesize the same shape — bare/wt sibling siblings, hidden dirs,
// nested two-level structure — and measure pure Go traversal + filtering.
func BenchmarkScanWalk_Small(b *testing.B)  { benchScanWalk(b, 5) }
func BenchmarkScanWalk_Medium(b *testing.B) { benchScanWalk(b, 50) }
func BenchmarkScanWalk_Large(b *testing.B)  { benchScanWalk(b, 200) }

func benchScanWalk(b *testing.B, projects int) {
	b.Helper()
	ws := benchfixture.Build(b, benchfixture.Options{
		Projects: projects,
		Cloned:   true, // gives us .bare/ siblings to filter out
	})
	personal := filepath.Join(ws.Root, "personal")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		entries, err := os.ReadDir(personal)
		if err != nil {
			b.Fatal(err)
		}
		var found int
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			if strings.HasSuffix(name, ".bare") || strings.Contains(name, "-wt-") {
				continue
			}
			found++
		}
		if found != projects {
			b.Fatalf("expected %d, got %d", projects, found)
		}
	}
}
