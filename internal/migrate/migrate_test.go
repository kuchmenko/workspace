package migrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/migrate"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestMigrateProject_HappyPath is the headline regression test for the
// `git worktree add --force <existing-non-empty-dir>` bug. Before the
// --no-checkout + pointer-swap rewrite, this test would fail with
// "fatal: '<path>' already exists" — because the working tree is non-empty
// before migrate runs (which is the realistic case for any human-used repo).
func TestMigrateProject_HappyPath(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main", "feature"})

	proj := &config.Project{
		Remote: plainPath, // local-only test, no real remote
		Path:   "myapp",
		Status: config.StatusActive,
	}

	res, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{
		Machine: "ci",
		Logf:    t.Logf,
	})
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}

	// 1. Bare repo exists at the canonical sibling location.
	barePath := plainPath + ".bare"
	if res.BarePath != barePath {
		t.Errorf("BarePath = %s, want %s", res.BarePath, barePath)
	}
	if !git.IsBare(barePath) {
		t.Errorf("%s is not a bare repo", barePath)
	}

	// 2. Main worktree at the original path is now a real worktree.
	if !git.IsRepo(plainPath) {
		t.Fatalf("%s is no longer a git repo after migrate", plainPath)
	}

	// 3. .git is a pointer file, not a directory.
	dotGit := filepath.Join(plainPath, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	if info.IsDir() {
		t.Errorf(".git is still a directory; expected a worktree pointer file")
	}

	// 4. The user's README is still there — bug 1 would have lost it.
	if _, err := os.Stat(filepath.Join(plainPath, "README.md")); err != nil {
		t.Errorf("README.md missing after migrate: %v", err)
	}

	// 5. Both branches survived into the bare.
	if !git.HasBranch(barePath, "main") {
		t.Errorf("main branch missing in bare")
	}
	if !git.HasBranch(barePath, "feature") {
		t.Errorf("feature branch missing in bare")
	}
	if res.BranchesPushed != 2 {
		t.Errorf("BranchesPushed = %d, want 2", res.BranchesPushed)
	}

	// 6. proj.DefaultBranch was filled in.
	if proj.DefaultBranch == "" {
		t.Errorf("proj.DefaultBranch was not set")
	}

	// 7. .git.migrating-* tmp files were cleaned up.
	entries, _ := os.ReadDir(plainPath)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".git.migrating-") {
			t.Errorf("leftover %s in main worktree", e.Name())
		}
	}
	// 8. .wt-tmp sibling was cleaned up.
	if _, err := os.Stat(plainPath + ".wt-tmp"); !os.IsNotExist(err) {
		t.Errorf("leftover .wt-tmp at %s", plainPath+".wt-tmp")
	}
}

// TestMigrateProject_DirtyAbortsWithoutWIP exercises the pre-flight: dirty
// trees must abort unless WIP is enabled.
func TestMigrateProject_DirtyAbortsWithoutWIP(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	testutil.AddDirty(t, plainPath)

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	_, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{Machine: "ci"})
	if err == nil {
		t.Fatal("expected error for dirty tree without WIP")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error mentions wrong reason: %v", err)
	}
}

// TestMigrateProject_DirtyWithWIP verifies the WIP-snapshot path: dirty
// state ends up on a wt/<machine>/migration-wip-* branch and the main
// worktree returns to the original branch.
func TestMigrateProject_DirtyWithWIP(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	testutil.AddDirty(t, plainPath)

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	res, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{
		WIP: true, Machine: "ci", Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("MigrateProject with WIP: %v", err)
	}
	if res.WIPBranch == "" {
		t.Errorf("WIPBranch not set in result")
	}
	if !strings.HasPrefix(res.WIPBranch, "wt/ci/migration-wip-") {
		t.Errorf("WIPBranch = %s, want wt/ci/migration-wip-*", res.WIPBranch)
	}
	// Main worktree should be back on main, not the WIP branch.
	br, _ := git.CurrentBranch(plainPath)
	if br != "main" {
		t.Errorf("main worktree on %s, want main", br)
	}
	// WIP branch must exist in the bare.
	if !git.HasBranch(plainPath+".bare", res.WIPBranch) {
		t.Errorf("WIP branch %s missing from bare", res.WIPBranch)
	}
}

// TestMigrateProject_StashAbortsWithoutFlag confirms stash entries refuse
// migration unless StashBranch is set.
func TestMigrateProject_StashAbortsWithoutFlag(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	testutil.AddStash(t, plainPath)

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	_, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{Machine: "ci"})
	if err == nil {
		t.Fatal("expected error for stash without StashBranch")
	}
	if !strings.Contains(err.Error(), "stash") {
		t.Errorf("error mentions wrong reason: %v", err)
	}
}

// TestMigrateProject_StashWithBranch verifies that with StashBranch enabled,
// each stash entry becomes a wt/<machine>/migration-stash-<ts>-N branch in
// the bare clone.
func TestMigrateProject_StashWithBranch(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	testutil.AddStash(t, plainPath)

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	res, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{
		StashBranch: true, Machine: "ci", Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("MigrateProject with StashBranch: %v", err)
	}
	if len(res.StashBranches) != 1 {
		t.Fatalf("StashBranches = %v, want 1 entry", res.StashBranches)
	}
	if !strings.HasPrefix(res.StashBranches[0], "wt/ci/migration-stash-") {
		t.Errorf("stash branch = %s, want wt/ci/migration-stash-*", res.StashBranches[0])
	}
	if !git.HasBranch(plainPath+".bare", res.StashBranches[0]) {
		t.Errorf("stash branch %s missing from bare", res.StashBranches[0])
	}
}

// TestMigrateProject_DetachedAbortsWithoutFlag exercises the detached HEAD
// pre-flight: must abort unless CheckoutDefault is set.
func TestMigrateProject_DetachedAbortsWithoutFlag(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	// Detach HEAD to current commit.
	head := git.RevParse(plainPath, "HEAD")
	testutil.RunGit(t, plainPath, "checkout", "--detach", head)

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	_, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{Machine: "ci"})
	if err == nil {
		t.Fatal("expected error for detached HEAD without CheckoutDefault")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("error mentions wrong reason: %v", err)
	}
}

// TestMigrateProject_DetachedWithCheckout verifies the detached recovery
// path: when the current commit is reachable from main, no preservation
// branch is created and the working tree ends up on main.
func TestMigrateProject_DetachedWithCheckout(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})
	head := git.RevParse(plainPath, "HEAD")
	testutil.RunGit(t, plainPath, "checkout", "--detach", head)

	proj := &config.Project{
		Remote: plainPath, Path: "myapp", Status: config.StatusActive,
		DefaultBranch: "main",
	}
	res, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{
		CheckoutDefault: true, Machine: "ci", Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("MigrateProject with CheckoutDefault: %v", err)
	}
	// Reachable from main → no preservation branch.
	if res.DetachedBranch != "" {
		t.Errorf("DetachedBranch = %s, want empty (commit reachable from main)", res.DetachedBranch)
	}
	br, _ := git.CurrentBranch(plainPath)
	if br != "main" {
		t.Errorf("main worktree on %s, want main", br)
	}
}

// TestMigrateProject_DetachedPreservesOrphan verifies that when the
// detached commit is NOT reachable from any branch, a preservation branch
// is created in the bare.
func TestMigrateProject_DetachedPreservesOrphan(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})

	// Make an orphan commit on a detached HEAD that no branch points at.
	testutil.RunGit(t, plainPath, "checkout", "--detach", "main")
	if err := os.WriteFile(filepath.Join(plainPath, "orphan.txt"), []byte("orphan\n"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	testutil.RunGit(t, plainPath, "add", "orphan.txt")
	testutil.RunGit(t, plainPath, "commit", "-m", "orphan commit")

	proj := &config.Project{
		Remote: plainPath, Path: "myapp", Status: config.StatusActive,
		DefaultBranch: "main",
	}
	res, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{
		CheckoutDefault: true, Machine: "ci", Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}
	if res.DetachedBranch == "" {
		t.Fatal("DetachedBranch should be set when commit isn't reachable from any branch")
	}
	if !git.HasBranch(plainPath+".bare", res.DetachedBranch) {
		t.Errorf("preserved branch %s missing from bare", res.DetachedBranch)
	}
}

// TestMigrateProject_AlreadyMigrated returns ErrAlreadyMigrated as a
// recoverable signal, not a hard error.
func TestMigrateProject_AlreadyMigrated(t *testing.T) {
	wsRoot := t.TempDir()
	plainPath := testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})

	proj := &config.Project{Remote: plainPath, Path: "myapp", Status: config.StatusActive}
	if _, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{Machine: "ci"}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	_, err := migrate.MigrateProject(wsRoot, "myapp", proj, migrate.Options{Machine: "ci"})
	if err != migrate.ErrAlreadyMigrated {
		t.Errorf("second migrate: got %v, want ErrAlreadyMigrated", err)
	}
}
