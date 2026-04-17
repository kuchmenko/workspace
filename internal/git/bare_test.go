package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestHasFetchRefspec_AbsentAfterCloneBare captures the baseline behavior
// that motivates the whole fix: `git clone --bare` does NOT configure
// remote.origin.fetch. Without this refspec, `git fetch` only updates
// FETCH_HEAD and branch@{u} cannot resolve.
func TestHasFetchRefspec_AbsentAfterCloneBare(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")
	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")

	if err := git.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}

	if git.HasFetchRefspec(barePath) {
		t.Fatal("expected fresh clone --bare to have NO remote.origin.fetch; this is the bug we're fixing")
	}
}

// TestSetFetchRefspec_MakesFetchPopulateOriginRefs is the core regression
// test for issue #14. Before the fix, `git fetch --all` against a bare
// clone only updated FETCH_HEAD and left refs/remotes/origin/* empty.
// After SetFetchRefspec, the same fetch populates origin/* as expected.
func TestSetFetchRefspec_MakesFetchPopulateOriginRefs(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")

	// Push an extra branch to origin so there's something to fetch
	// besides main.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.RunGit(t, filepath.Dir(seed), "clone", remote, seed)
	testutil.RunGit(t, seed, "checkout", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(seed, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, seed, "add", "x.txt")
	testutil.RunGit(t, seed, "commit", "-m", "x")
	testutil.RunGit(t, seed, "push", "origin", "feature/x")

	// Bare clone the remote.
	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	if err := git.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}

	// Baseline: no origin/* refs yet (clone --bare writes to refs/heads/).
	if git.RevParse(barePath, "refs/remotes/origin/main") != "" {
		t.Fatal("unexpected: refs/remotes/origin/main already exists before SetFetchRefspec+Fetch")
	}

	// Apply the fix.
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("SetFetchRefspec: %v", err)
	}
	if !git.HasFetchRefspec(barePath) {
		t.Fatal("HasFetchRefspec false after SetFetchRefspec")
	}

	// Fetch — after the fix, this should populate refs/remotes/origin/*.
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if git.RevParse(barePath, "refs/remotes/origin/main") == "" {
		t.Error("refs/remotes/origin/main not populated after Fetch — the refspec fix didn't take effect")
	}
	if git.RevParse(barePath, "refs/remotes/origin/feature/x") == "" {
		t.Error("refs/remotes/origin/feature/x not populated — fetch refspec is not matching all heads")
	}
}

// TestSetFetchRefspec_Idempotent verifies that calling SetFetchRefspec
// twice is safe: the second call overwrites the single-valued setting
// rather than appending a duplicate.
func TestSetFetchRefspec_Idempotent(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")
	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	if err := git.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}

	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("first SetFetchRefspec: %v", err)
	}
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("second SetFetchRefspec: %v", err)
	}

	// Expect exactly one configured fetch value, not two.
	got := testutil.RunGit(t, barePath, "config", "--get-all", "remote.origin.fetch")
	want := "+refs/heads/*:refs/remotes/origin/*"
	if got != want {
		t.Errorf("remote.origin.fetch = %q, want exactly %q (single line)", got, want)
	}
}

// TestAheadBehind_AccurateAfterFix verifies the user-visible payoff of
// the refspec fix: in a worktree whose branch has an upstream configured
// via SetBranchUpstream, AheadBehind returns meaningful numbers rather
// than (0, 0, false). Before the fix, @{u} couldn't resolve because
// refs/remotes/origin/<branch> didn't exist.
func TestAheadBehind_AccurateAfterFix(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "proj", "main")

	tmp := t.TempDir()
	barePath := filepath.Join(tmp, "proj.bare")
	if err := git.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("SetFetchRefspec: %v", err)
	}
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := git.SetBranchUpstream(barePath, "main", "origin"); err != nil {
		t.Fatalf("SetBranchUpstream: %v", err)
	}

	// Attach a worktree for main.
	mainPath := filepath.Join(tmp, "proj")
	if err := git.WorktreeAdd(barePath, mainPath, "main", ""); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	// Immediately after attach, local main tracks origin/main at the same
	// SHA → (0, 0, true). The critical assertion is hasUpstream=true.
	ahead, behind, has := git.AheadBehind(mainPath, "main")
	if !has {
		t.Fatal("AheadBehind: has=false — @{u} still not resolving after the fix")
	}
	if ahead != 0 || behind != 0 {
		t.Errorf("AheadBehind: ahead=%d, behind=%d, want both 0", ahead, behind)
	}

	// Push a commit to origin from a separate clone so the main worktree
	// becomes behind.
	external := filepath.Join(tmp, "external")
	testutil.RunGit(t, tmp, "clone", remote, external)
	if err := os.WriteFile(filepath.Join(external, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, external, "add", "new.txt")
	testutil.RunGit(t, external, "commit", "-m", "remote-only commit")
	testutil.RunGit(t, external, "push", "origin", "main")

	// Re-fetch in the bare — with the refspec, origin/main advances.
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}

	ahead, behind, has = git.AheadBehind(mainPath, "main")
	if !has {
		t.Fatal("AheadBehind post-fetch: has=false")
	}
	if behind != 1 {
		t.Errorf("AheadBehind post-fetch: behind=%d, want 1", behind)
	}
	if ahead != 0 {
		t.Errorf("AheadBehind post-fetch: ahead=%d, want 0", ahead)
	}
}
