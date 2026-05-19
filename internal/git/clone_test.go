package git_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestCloneIntoLayout_HappyPath(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	proj := &config.Project{
		Remote: remote,
		Path:   "personal/myapp",
		Status: config.StatusActive,
	}
	res, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{Logf: t.Logf})
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

	// Issue #14: remote.origin.fetch must be set so subsequent fetches
	// populate refs/remotes/origin/* (without it, AheadBehind and ff-pull
	// silently break). `git clone --bare` omits this key — CloneIntoLayout
	// has to install it explicitly.
	if !git.HasFetchRefspec(bare) {
		t.Error("remote.origin.fetch not set after CloneIntoLayout — issue #14 regression")
	}
	gotRefspec := testutil.RunGit(t, bare, "config", "--get-all", "remote.origin.fetch")
	wantRefspec := "+refs/heads/*:refs/remotes/origin/*"
	if gotRefspec != wantRefspec {
		t.Errorf("remote.origin.fetch = %q, want %q", gotRefspec, wantRefspec)
	}
}

func TestCloneIntoLayout_AlreadyCloned(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")
	proj := &config.Project{Remote: remote, Path: "myapp", Status: config.StatusActive}

	if _, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{}); err != nil {
		t.Fatalf("first clone: %v", err)
	}
	_, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{})
	if !errors.Is(err, git.ErrAlreadyCloned) {
		t.Errorf("second clone: got %v, want ErrAlreadyCloned", err)
	}
}

func TestCloneIntoLayout_NeedsMigration(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	testutil.InitFakePlainCheckout(t, wsRoot, "myapp", []string{"main"})

	proj := &config.Project{Remote: remote, Path: "myapp", Status: config.StatusActive}
	_, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{})
	if !errors.Is(err, git.ErrNeedsMigration) {
		t.Errorf("got %v, want ErrNeedsMigration", err)
	}
}

func TestCloneIntoLayout_PathBlocked(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	if err := os.MkdirAll(filepath.Join(wsRoot, "personal"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsRoot, "personal", "myapp"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	proj := &config.Project{Remote: remote, Path: "personal/myapp", Status: config.StatusActive}
	_, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{})
	if !errors.Is(err, git.ErrPathBlocked) {
		t.Errorf("got %v, want ErrPathBlocked", err)
	}
}

func TestCloneIntoLayout_DefaultBranchPreSet(t *testing.T) {
	wsRoot := t.TempDir()
	remote := testutil.InitFakeRemote(t, "myapp", "main")

	proj := &config.Project{
		Remote:        remote,
		Path:          "myapp",
		Status:        config.StatusActive,
		DefaultBranch: "main",
		// explicit
	}
	res, err := git.CloneIntoLayout(wsRoot, "myapp", proj, git.CloneOptions{})
	if err != nil {
		t.Fatalf("CloneIntoLayout: %v", err)
	}
	if res.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %s, want main", res.DefaultBranch)
	}
}
