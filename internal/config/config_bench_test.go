package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

// BenchmarkLoad measures TOML decode + post-decode normalization
// (legacy autopush migration, default-status assignment) across realistic
// workspace sizes. The hot input is a synthetic workspace.toml — the
// bench string itself stays cached between iterations.
func BenchmarkLoad_Small(b *testing.B)  { benchLoad(b, 5, 1) }
func BenchmarkLoad_Medium(b *testing.B) { benchLoad(b, 50, 3) }
func BenchmarkLoad_Large(b *testing.B)  { benchLoad(b, 500, 5) }

func benchLoad(b *testing.B, projects, branchesPerProject int) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.toml"),
		[]byte(synthesizeWorkspaceTOML(projects, branchesPerProject)), 0o644); err != nil {
		b.Fatalf("write toml: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := config.Load(root); err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}

// BenchmarkSave measures TOML encode + atomic-write cost. Includes the
// rename+fsync costs that dominate on slow disks.
func BenchmarkSave_Small(b *testing.B)  { benchSave(b, 5, 1) }
func BenchmarkSave_Medium(b *testing.B) { benchSave(b, 50, 3) }
func BenchmarkSave_Large(b *testing.B)  { benchSave(b, 500, 5) }

func benchSave(b *testing.B, projects, branchesPerProject int) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.toml"),
		[]byte(synthesizeWorkspaceTOML(projects, branchesPerProject)), 0o644); err != nil {
		b.Fatalf("write toml: %v", err)
	}
	ws, err := config.Load(root)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := config.Save(root, ws); err != nil {
			b.Fatalf("save: %v", err)
		}
	}
}

// BenchmarkValidate guards against accidental O(N²) regressions in the
// duplicate-branch scan. The current impl is O(total branches) per
// project; if anyone introduces a nested per-issue scan it'll show here.
func BenchmarkValidate_Small(b *testing.B)  { benchValidate(b, 5, 1) }
func BenchmarkValidate_Medium(b *testing.B) { benchValidate(b, 50, 5) }
func BenchmarkValidate_Large(b *testing.B)  { benchValidate(b, 500, 10) }

func benchValidate(b *testing.B, projects, branchesPerProject int) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.toml"),
		[]byte(synthesizeWorkspaceTOML(projects, branchesPerProject)), 0o644); err != nil {
		b.Fatalf("write toml: %v", err)
	}
	ws, err := config.Load(root)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ws.Validate()
	}
}

// BenchmarkClaimBranch measures one ClaimBranch op on a project that
// already has K branches — the linear scan in LookupBranch dominates.
// Catches regressions if someone replaces the slice with something
// pathological without a matching index.
func BenchmarkClaimBranch_Append(b *testing.B) { benchClaimBranch(b, 50, false) }
func BenchmarkClaimBranch_Update(b *testing.B) { benchClaimBranch(b, 50, true) }

func benchClaimBranch(b *testing.B, existingBranches int, hitExisting bool) {
	b.Helper()
	proj := &config.Project{
		Remote:        "git@example.invalid:bench/proj.git",
		Path:          "personal/proj",
		Status:        config.StatusActive,
		Category:      config.CategoryPersonal,
		DefaultBranch: "main",
	}
	for i := 0; i < existingBranches; i++ {
		proj.Branches = append(proj.Branches, config.BranchMeta{
			Name:     fmt.Sprintf("feat/branch-%d", i),
			Machines: []string{"bench"},
		})
	}
	target := "feat/new-branch"
	if hitExisting {
		target = "feat/branch-0"
	}
	// Pre-grow capacity so append() inside the measured loop never
	// reallocates — we want pure append+copy cost, not allocator cost.
	// Truncate len back to existingBranches so the first iteration starts
	// from the same state every iteration of the append benchmark.
	if !hitExisting {
		proj.Branches = append(proj.Branches, config.BranchMeta{})
		proj.Branches = proj.Branches[:existingBranches]
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// For the append path, every iteration must start with a slice
		// that does NOT contain `target`. Without this reset, iter 1
		// appends `feat/new-branch` once, and iters 2..N take the
		// existing-branch update path through LookupBranch — silently
		// turning the bench into a duplicate of BenchmarkClaimBranch_Update.
		if !hitExisting {
			b.StopTimer()
			proj.Branches = proj.Branches[:existingBranches]
			b.StartTimer()
		}
		_, _ = proj.ClaimBranch(target, "bench")
	}
}

// synthesizeWorkspaceTOML produces a realistic-looking workspace.toml
// without invoking any of the in-prod write paths. Output shape mirrors
// what the CLI writes after `ws add` + `ws worktree add`.
func synthesizeWorkspaceTOML(projects, branchesPerProject int) string {
	var sb strings.Builder
	sb.WriteString("[meta]\nversion = 1\nroot = \".\"\n\n")
	sb.WriteString("[daemon]\npoll_interval = \"5m\"\nstale_threshold = \"30d\"\nauto_sync = true\nwatch_dirs = false\n\n")
	for i := 0; i < projects; i++ {
		fmt.Fprintf(&sb, "[projects.proj-%03d]\n", i)
		fmt.Fprintf(&sb, "remote = \"git@example.invalid:bench/proj-%03d.git\"\n", i)
		fmt.Fprintf(&sb, "path = \"personal/proj-%03d\"\n", i)
		fmt.Fprintf(&sb, "status = \"active\"\n")
		fmt.Fprintf(&sb, "category = \"personal\"\n")
		fmt.Fprintf(&sb, "default_branch = \"main\"\n\n")
		for j := 0; j < branchesPerProject; j++ {
			fmt.Fprintf(&sb, "[[projects.proj-%03d.branches]]\n", i)
			fmt.Fprintf(&sb, "  name = \"feat/branch-%d\"\n", j)
			fmt.Fprintf(&sb, "  machines = [\"bench\"]\n")
			fmt.Fprintf(&sb, "  created_by = \"bench\"\n")
			fmt.Fprintf(&sb, "  created_at = \"2026-01-01T00:00:00Z\"\n\n")
		}
	}
	return sb.String()
}
