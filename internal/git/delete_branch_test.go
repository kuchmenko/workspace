package git_test

import (
	"path/filepath"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

func TestDeleteLocalAndRemoteBranch(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "remote", "main")
	repo := filepath.Join(t.TempDir(), "repo")
	testutil.RunGit(t, filepath.Dir(repo), "clone", remote, repo)
	testutil.RunGit(t, repo, "checkout", "-b", "feat/remove")
	testutil.RunGit(t, repo, "push", "-u", "origin", "feat/remove")
	testutil.RunGit(t, repo, "checkout", "main")

	oid := testutil.RunGit(t, repo, "rev-parse", "refs/remotes/origin/feat/remove")
	localOID := testutil.RunGit(t, repo, "rev-parse", "refs/heads/feat/remove")
	if err := git.DeleteRemoteBranch(repo, "origin", "feat/remove", oid); err != nil {
		t.Fatal(err)
	}
	if err := git.DeleteLocalBranch(repo, "feat/remove", localOID); err != nil {
		t.Fatal(err)
	}
	if git.HasBranch(repo, "feat/remove") || testutil.RunGitTry(t, remote, "show-ref", "--verify", "refs/heads/feat/remove") == nil {
		t.Fatal("branch still exists locally or remotely")
	}
}

func TestDeleteLocalBranchRejectsStaleExpectedOID(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "remote", "main")
	repo := filepath.Join(t.TempDir(), "repo")
	testutil.RunGit(t, filepath.Dir(repo), "clone", remote, repo)
	testutil.RunGit(t, repo, "checkout", "-b", "feat/race")
	staleOID := testutil.RunGit(t, repo, "rev-parse", "HEAD")
	testutil.AddDirty(t, repo)
	testutil.RunGit(t, repo, "add", ".")
	testutil.RunGit(t, repo, "commit", "-m", "advance")
	testutil.RunGit(t, repo, "checkout", "main")

	if err := git.DeleteLocalBranch(repo, "feat/race", staleOID); err == nil {
		t.Fatal("stale expected OID deleted changed local branch")
	}
	if !git.HasBranch(repo, "feat/race") {
		t.Fatal("changed local branch was deleted")
	}
}

func TestFetchRemoteBranchRejectsMissingBranch(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "remote", "main")
	repo := filepath.Join(t.TempDir(), "repo")
	testutil.RunGit(t, filepath.Dir(repo), "clone", remote, repo)
	if _, err := git.FetchRemoteBranch(repo, "origin", "feat/missing"); err == nil {
		t.Fatal("missing remote branch was accepted")
	}
}

func TestDeleteRemoteBranchRejectsStaleLease(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "remote", "main")
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	other := filepath.Join(root, "other")
	testutil.RunGit(t, root, "clone", remote, repo)
	testutil.RunGit(t, repo, "checkout", "-b", "feat/race")
	testutil.RunGit(t, repo, "push", "-u", "origin", "feat/race")
	oid, err := git.FetchRemoteBranch(repo, "origin", "feat/race")
	if err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, root, "clone", remote, other)
	testutil.RunGit(t, other, "checkout", "feat/race")
	testutil.AddDirty(t, other)
	testutil.RunGit(t, other, "add", ".")
	testutil.RunGit(t, other, "commit", "-m", "advance")
	testutil.RunGit(t, other, "push", "origin", "feat/race")
	if err := git.DeleteRemoteBranch(repo, "origin", "feat/race", oid); err == nil {
		t.Fatal("stale lease deleted advanced branch")
	}
	if testutil.RunGitTry(t, remote, "show-ref", "--verify", "refs/heads/feat/race") != nil {
		t.Fatal("advanced remote branch was deleted")
	}
}
