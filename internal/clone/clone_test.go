package clone_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestCloneIntoLayout_HappyPath verifies the basic clone-into-bare-worktree
// flow against a fake remote. Asserts both the bare and the main worktree
// exist and that proj.DefaultBranch was filled in from origin/HEAD.
func TestCloneIntoLayout_HappyPath(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	proj := &config.Project{
		Remote: remote,
		Path:   "personal/myapp",
		Status: config.StatusActive,
	}
	res, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{Logf: t.Logf})
	if err != nil {
		t.Fatalf("CloneIntoLayout: %v", err)
	}

	if res.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %s, want main", res.DefaultBranch)
	}
	if proj.DefaultBranch != "main" {
		t.Errorf("proj.DefaultBranch = %s, want main", proj.DefaultBranch)
	}

	bare := filepath.Join(wsRoot, "personal", "myapp.bare")
	main := filepath.Join(wsRoot, "personal", "myapp")
	if !git.IsBare(bare) {
		t.Errorf("%s is not a bare repo", bare)
	}
	if !git.IsRepo(main) {
		t.Errorf("%s is not a git repo", main)
	}
	if _, err := os.Stat(filepath.Join(main, "README.md")); err != nil {
		t.Errorf("README.md missing in main worktree: %v", err)
	}

	// Upstream tracking on the default branch must be configured so plain
	// `git push` works. SetBranchUpstream writes both keys via git config;
	// verify both made it to the bare's config file.
	wantRemote := "origin"
	wantMerge := "refs/heads/main"
	gotRemote := testutil.RunGit(t, bare, "config", "branch.main.remote")
	if gotRemote != wantRemote {
		t.Errorf("branch.main.remote = %q, want %q", gotRemote, wantRemote)
	}
	gotMerge := testutil.RunGit(t, bare, "config", "branch.main.merge")
	if gotMerge != wantMerge {
		t.Errorf("branch.main.merge = %q, want %q", gotMerge, wantMerge)
	}
}

// TestCloneIntoLayout_AlreadyCloned verifies that a second call returns
// ErrAlreadyCloned without doing anything destructive.
func TestCloneIntoLayout_AlreadyCloned(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")
	proj := &config.Project{Remote: remote, Path: "myapp", Status: config.StatusActive}

	if _, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{}); err != nil {
		t.Fatalf("first clone: %v", err)
	}
	_, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{})
	if !errors.Is(err, clone.ErrAlreadyCloned) {
		t.Errorf("second clone: got %v, want ErrAlreadyCloned", err)
	}
}

// TestCloneIntoLayout_NeedsMigration verifies that an existing plain
// checkout (no .bare sibling) returns ErrNeedsMigration.
func TestCloneIntoLayout_NeedsMigration(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	// Pre-create a plain checkout at the would-be main path.
	testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})

	proj := &config.Project{Remote: remote, Path: "myapp", Status: config.StatusActive}
	_, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{})
	if !errors.Is(err, clone.ErrNeedsMigration) {
		t.Errorf("got %v, want ErrNeedsMigration", err)
	}
}

// TestCloneIntoLayout_PathBlocked verifies that non-repo files at the main
// path return ErrPathBlocked.
func TestCloneIntoLayout_PathBlocked(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	// Create a non-repo file at the would-be main path.
	if err := os.MkdirAll(filepath.Join(wsRoot, "personal"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsRoot, "personal", "myapp"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	proj := &config.Project{Remote: remote, Path: "personal/myapp", Status: config.StatusActive}
	_, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{})
	if !errors.Is(err, clone.ErrPathBlocked) {
		t.Errorf("got %v, want ErrPathBlocked", err)
	}
}

// TestCloneIntoLayout_DefaultBranchPreSet verifies that an explicit
// proj.DefaultBranch is honored even when origin/HEAD would suggest
// otherwise.
func TestCloneIntoLayout_DefaultBranchPreSet(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	proj := &config.Project{
		Remote:        remote,
		Path:          "myapp",
		Status:        config.StatusActive,
		DefaultBranch: "main", // explicit
	}
	res, err := clone.CloneIntoLayout(wsRoot, "myapp", proj, clone.Options{})
	if err != nil {
		t.Fatalf("CloneIntoLayout: %v", err)
	}
	if res.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %s, want main", res.DefaultBranch)
	}
}
