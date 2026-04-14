package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestWorktreeAdd_ExistingBranch verifies that WorktreeAdd with
// createFromBase="" correctly checks out a branch that already exists
// in the bare repo. This is the core scenario of issue #8: another
// machine pushed a branch, a targeted refspec fetch brought it into
// refs/heads/, and we create a worktree from it without -b.
func TestWorktreeAdd_ExistingBranch(t *testing.T) {
	// 1. Create a fake remote with a seed commit on "main".
	remote := testutil.InitFakeRemote(t, "proj", "main")

	// 2. Clone it as a bare repo (mimics ws bootstrap / migrate).
	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	testutil.RunGit(t, tmp, "clone", "--bare", remote, barePath)

	// 3. Push a feature branch to the remote from a separate clone,
	//    simulating work done on another machine.
	otherClone := filepath.Join(tmp, "other")
	testutil.RunGit(t, tmp, "clone", remote, otherClone)
	testutil.RunGit(t, otherClone, "checkout", "-b", "wt/other-machine/feature")
	if err := os.WriteFile(filepath.Join(otherClone, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, otherClone, "add", "feature.txt")
	testutil.RunGit(t, otherClone, "commit", "-m", "feature work")
	testutil.RunGit(t, otherClone, "push", "origin", "wt/other-machine/feature")

	// 4. Targeted refspec fetch — git clone --bare does NOT create a
	//    fetch refspec, so `git fetch --all` would miss new branches.
	//    This matches what ws worktree new does.
	branch := "wt/other-machine/feature"
	refspec := "+refs/heads/" + branch + ":refs/heads/" + branch
	if err := git.FetchRefspec(barePath, "origin", refspec); err != nil {
		t.Fatalf("FetchRefspec: %v", err)
	}

	// Sanity: the branch must now exist in the bare.
	if !git.HasBranch(barePath, branch) {
		t.Fatal("expected branch to exist in bare after targeted fetch")
	}

	// 5. Create a worktree from the existing branch (no -b).
	wtPath := filepath.Join(tmp, "proj-wt-other-machine-feature")
	if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
		t.Fatalf("WorktreeAdd existing branch: %v", err)
	}

	// 6. Verify the worktree exists and is on the correct branch.
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree directory does not exist: %v", err)
	}
	gotBranch, err := git.CurrentBranch(wtPath)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if gotBranch != branch {
		t.Fatalf("expected branch %s, got %s", branch, gotBranch)
	}

	// 7. Verify the feature file from the remote branch is present.
	if _, err := os.Stat(filepath.Join(wtPath, "feature.txt")); err != nil {
		t.Fatal("feature.txt not present in worktree — branch content not checked out")
	}
}

// TestWorktreeAdd_NewBranch verifies the existing behavior: creating a
// brand-new branch from a base ref still works as before.
func TestWorktreeAdd_NewBranch(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")

	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	testutil.RunGit(t, tmp, "clone", "--bare", remote, barePath)

	// The branch does not exist yet.
	if git.HasBranch(barePath, "wt/linux/new-topic") {
		t.Fatal("branch should not exist before creation")
	}

	wtPath := filepath.Join(tmp, "proj-wt-linux-new-topic")
	if err := git.WorktreeAdd(barePath, wtPath, "wt/linux/new-topic", "main"); err != nil {
		t.Fatalf("WorktreeAdd new branch: %v", err)
	}

	gotBranch, err := git.CurrentBranch(wtPath)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if gotBranch != "wt/linux/new-topic" {
		t.Fatalf("expected branch wt/linux/new-topic, got %s", gotBranch)
	}

	// The new branch should now exist in the bare.
	if !git.HasBranch(barePath, "wt/linux/new-topic") {
		t.Fatal("branch should exist in bare after creation")
	}
}

// TestWorktreeNew_FetchAndDetect is an end-to-end simulation of the
// issue #8 flow: a branch is pushed to origin from another machine,
// the local bare fetches it via targeted refspec, and WorktreeAdd
// checks it out without -b. Also verifies upstream tracking setup.
func TestWorktreeNew_FetchAndDetect(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")

	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	testutil.RunGit(t, tmp, "clone", "--bare", remote, barePath)

	branch := "wt/archlinux/data-api"

	// Branch does NOT exist before fetch of the new push.
	if git.HasBranch(barePath, branch) {
		t.Fatal("branch should not exist before push+fetch")
	}

	// Simulate another machine pushing via a separate clone.
	otherClone := filepath.Join(tmp, "machine-b")
	testutil.RunGit(t, tmp, "clone", remote, otherClone)
	testutil.RunGit(t, otherClone, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(otherClone, "api.go"), []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, otherClone, "add", "api.go")
	testutil.RunGit(t, otherClone, "commit", "-m", "data api scaffold")
	testutil.RunGit(t, otherClone, "push", "origin", branch)

	// Targeted refspec fetch — the same approach ws worktree new uses.
	refspec := "+refs/heads/" + branch + ":refs/heads/" + branch
	if err := git.FetchRefspec(barePath, "origin", refspec); err != nil {
		t.Fatalf("FetchRefspec: %v", err)
	}

	// Now the branch should be visible.
	if !git.HasBranch(barePath, branch) {
		t.Fatal("branch should exist in bare after targeted fetch")
	}

	// Create worktree from the existing branch.
	wtPath := filepath.Join(tmp, "proj-wt-archlinux-data-api")
	if err := git.WorktreeAdd(barePath, wtPath, branch, ""); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	// Verify branch and content.
	gotBranch, err := git.CurrentBranch(wtPath)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if gotBranch != branch {
		t.Fatalf("expected %s, got %s", branch, gotBranch)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "api.go")); err != nil {
		t.Fatal("api.go not present — remote branch content not checked out")
	}

	// Verify upstream tracking can be set up (same as CLI does).
	if err := git.SetBranchUpstream(barePath, branch, "origin"); err != nil {
		t.Fatalf("SetBranchUpstream: %v", err)
	}
}

// TestFetchRefspec_NonexistentBranch verifies that fetching a branch
// that doesn't exist on origin fails gracefully (returns an error but
// doesn't panic or corrupt state).
func TestFetchRefspec_NonexistentBranch(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")

	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	testutil.RunGit(t, tmp, "clone", "--bare", remote, barePath)

	refspec := "+refs/heads/wt/linux/no-such-branch:refs/heads/wt/linux/no-such-branch"
	err := git.FetchRefspec(barePath, "origin", refspec)

	// Should fail (branch doesn't exist on origin) but not panic.
	if err == nil {
		t.Fatal("expected error when fetching non-existent branch")
	}

	// Branch should not exist locally.
	if git.HasBranch(barePath, "wt/linux/no-such-branch") {
		t.Fatal("branch should not exist after failed fetch")
	}
}
