package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestCloneBareLocalContextCleansFailedDestination(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "broken-local", "main")
	removeHeadObject(t, remote)
	destination := filepath.Join(t.TempDir(), "clone.git")

	if err := git.CloneBareLocalContext(context.Background(), remote, destination); err == nil {
		t.Fatal("CloneBareLocalContext succeeded for corrupt repository")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination remains after failed clone: %v", statErr)
	}
}

func removeHeadObject(t *testing.T, repository string) {
	t.Helper()
	head := testutil.RunGit(t, repository, "rev-parse", "HEAD")
	object := filepath.Join(repository, "objects", head[:2], head[2:])
	if err := os.Remove(object); err != nil {
		t.Fatalf("remove source object: %v", err)
	}
}

func TestCloneIntoLayoutContextCleansFailedLayout(t *testing.T) {
	root := t.TempDir()
	remote := testutil.InitFakeRemote(t, "project", "main")
	project := &config.Project{
		Remote:        remote,
		Path:          "personal/project",
		DefaultBranch: "missing",
	}

	if _, err := git.CloneIntoLayoutContext(context.Background(), root, "project", project, git.CloneOptions{}); err == nil {
		t.Fatal("CloneIntoLayoutContext succeeded with missing default branch")
	}
	for _, path := range []string{
		filepath.Join(root, "personal", "project"),
		filepath.Join(root, "personal", "project.bare"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("clone path remains after failure: %s", path)
		}
	}
}

func TestCloneIntoLayoutContextCleansCanceledLayout(t *testing.T) {
	root := t.TempDir()
	project := &config.Project{
		Remote: "/missing/repository",
		Path:   "personal/project",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := git.CloneIntoLayoutContext(ctx, root, "project", project, git.CloneOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CloneIntoLayoutContext error = %v, want context.Canceled", err)
	}
	for _, path := range []string{
		filepath.Join(root, "personal", "project"),
		filepath.Join(root, "personal", "project.bare"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("clone path remains after cancellation: %s", path)
		}
	}
}

func TestCloneIntoLayoutContextConcurrentOwnerPreservesLayout(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "concurrent-layout", "main")
	for attempt := 0; attempt < 10; attempt++ {
		root := t.TempDir()
		start := make(chan struct{})
		errors := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				project := &config.Project{Remote: remote, Path: "project", Status: config.StatusActive}
				<-start
				_, err := git.CloneIntoLayoutContext(context.Background(), root, "project", project, git.CloneOptions{})
				errors <- err
			}()
		}
		close(start)
		workers.Wait()
		close(errors)
		successes := 0
		for err := range errors {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("attempt %d successes = %d, want 1", attempt, successes)
		}
		if !git.IsBare(filepath.Join(root, "project.bare")) || !git.IsRepo(filepath.Join(root, "project")) {
			t.Fatalf("attempt %d winning layout was removed", attempt)
		}
	}
}

func TestContextOperationsReturnCancellation(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "project", "main")
	repo := filepath.Join(t.TempDir(), "project")
	testutil.RunGit(t, filepath.Dir(repo), "clone", remote, repo)

	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{"fetch", func(ctx context.Context) error { return git.FetchContext(ctx, repo) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := test.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestURLNetworkOperationsIgnoreChangedOriginConfig(t *testing.T) {
	frozen := testutil.InitFakeRemote(t, "frozen", "main")
	redirected := testutil.InitFakeRemote(t, "redirected", "main")
	repo := filepath.Join(t.TempDir(), "project")
	testutil.RunGit(t, filepath.Dir(repo), "clone", frozen, repo)
	testutil.RunGit(t, repo, "remote", "set-url", "origin", redirected)

	frozenSeed := cloneAndCommit(t, frozen, "frozen.txt")
	redirectedSeed := cloneAndCommit(t, redirected, "redirected.txt")
	if err := git.FastForwardURLBranchContext(context.Background(), repo, frozen, "main"); err != nil {
		t.Fatalf("FastForwardURLBranchContext: %v", err)
	}
	if got := git.RevParse(repo, "HEAD"); got != frozenSeed || got == redirectedSeed {
		t.Fatalf("HEAD = %s, frozen = %s, redirected = %s", got, frozenSeed, redirectedSeed)
	}

	if err := os.WriteFile(filepath.Join(repo, "local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, repo, "add", "local.txt")
	testutil.RunGit(t, repo, "commit", "-m", "local")
	localHead := git.RevParse(repo, "HEAD")
	if err := git.PushURLBranchContext(context.Background(), repo, frozen, "main"); err != nil {
		t.Fatalf("PushURLBranchContext: %v", err)
	}
	if got := git.RevParse(frozen, "refs/heads/main"); got != localHead {
		t.Fatalf("frozen remote head = %s, want %s", got, localHead)
	}
	if got := git.RevParse(redirected, "refs/heads/main"); got != redirectedSeed {
		t.Fatalf("redirected remote changed to %s, want %s", got, redirectedSeed)
	}
}

func TestRebaseURLBranchIgnoresChangedOriginConfig(t *testing.T) {
	frozen := testutil.InitFakeRemote(t, "rebase-frozen", "main")
	redirected := testutil.InitFakeRemote(t, "rebase-redirected", "main")
	repo := filepath.Join(t.TempDir(), "project")
	testutil.RunGit(t, filepath.Dir(repo), "clone", frozen, repo)
	testutil.RunGit(t, repo, "remote", "set-url", "origin", redirected)
	testutil.RunGit(t, repo, "commit", "--allow-empty", "-m", "local")
	frozenHead := cloneAndCommit(t, frozen, "remote.txt")
	redirectedHead := cloneAndCommit(t, redirected, "redirected.txt")

	if err := git.RebaseURLBranchContext(context.Background(), repo, frozen, "main"); err != nil {
		t.Fatalf("RebaseURLBranchContext: %v", err)
	}
	if base := testutil.RunGit(t, repo, "merge-base", "HEAD", frozenHead); base != frozenHead {
		t.Fatalf("rebased onto %s, want frozen head %s", base, frozenHead)
	}
	if got := git.RevParse(repo, "refs/remotes/origin/main"); got != frozenHead || got == redirectedHead {
		t.Fatalf("origin/main = %s, frozen = %s, redirected = %s", got, frozenHead, redirectedHead)
	}
}

func cloneAndCommit(t *testing.T, remote, filename string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "seed")
	testutil.RunGit(t, filepath.Dir(repo), "clone", remote, repo)
	if err := os.WriteFile(filepath.Join(repo, filename), []byte(filename+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, repo, "add", filename)
	testutil.RunGit(t, repo, "commit", "-m", filename)
	testutil.RunGit(t, repo, "push", "origin", "main")
	return git.RevParse(repo, "HEAD")
}
