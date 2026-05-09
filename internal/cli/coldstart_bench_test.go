package cli_test

import (
	"testing"

	"github.com/kuchmenko/workspace/internal/cli"
)

// BenchmarkNewRootCmd measures the cobra-tree construction cost paid on
// every `ws <cmd>` invocation. This is the dominant Go-level slice of
// CLI cold-start (init() trace shows up in L3, not here).
//
// Regressions here typically come from new subcommands with heavy init
// (e.g. github API client constructed at package level instead of lazily)
// or from cobra middleware that walks the tree unnecessarily.
func BenchmarkNewRootCmd(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = cli.NewRootCmd()
	}
}

// BenchmarkRootCmd_Help measures cobra's help-rendering path, which is
// what `ws --help` and any error message touch. Useful as a stable proxy
// for "command-tree walk" cost.
func BenchmarkRootCmd_Help(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		root := cli.NewRootCmd()
		root.SetArgs([]string{"--help"})
		// Suppress output: we measure the walk, not the writes.
		root.SetOut(discardWriter{})
		root.SetErr(discardWriter{})
		_ = root.Execute()
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
