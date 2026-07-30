package conflict_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/conflict"
)

func BenchmarkRecordFresh(b *testing.B) {
	b.ReportAllocs()
	dir := b.TempDir()
	b.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	store, err := conflict.Open()
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	confPath, err := conflict.Path()
	if err != nil {
		b.Fatalf("resolve path: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		_ = os.Remove(confPath)
		b.StartTimer()
		_, err := store.Record(conflict.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "main",
			Kind:      conflict.KindNeedsBootstrap,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordDuplicateMatch10(b *testing.B)  { benchDuplicateMatch(b, 10) }
func BenchmarkRecordDuplicateMatch100(b *testing.B) { benchDuplicateMatch(b, 100) }

func benchDuplicateMatch(b *testing.B, existing int) {
	b.Helper()
	store := storeAt(b)
	for i := 0; i < existing; i++ {
		if _, err := store.Record(conflict.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "main",
			Kind:      conflict.KindNeedsBootstrap,
		}); err != nil {
			b.Fatal(err)
		}
	}
	target := conflict.Conflict{
		Workspace:  "/ws",
		Project:    fmt.Sprintf("proj-%d", existing-1),
		Branch:     "main",
		Kind:       conflict.KindNeedsBootstrap,
		DetectedAt: time.Now().UTC(),
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := store.Record(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkList10(b *testing.B)  { benchList(b, 10) }
func BenchmarkList100(b *testing.B) { benchList(b, 100) }

func benchList(b *testing.B, count int) {
	b.Helper()
	store := storeAt(b)
	for i := 0; i < count; i++ {
		if _, err := store.Record(conflict.Conflict{
			Workspace: "/ws",
			Project:   fmt.Sprintf("proj-%d", i),
			Branch:    "feat/branch",
			Kind:      conflict.KindBranchOrphan,
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := store.List(); err != nil {
			b.Fatal(err)
		}
	}
}

func storeAt(b *testing.B) *conflict.Store {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	store, err := conflict.Open()
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	return store
}
