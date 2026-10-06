package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestMaterializerClonesAndCreatesPublishedWorktrees(t *testing.T) {
	useTestGitEnvironment(t)
	remote := testutil.InitFakeRemote(t, "app", "main")
	seed := filepath.Join(filepath.Dir(remote), "seed")
	testutil.RunGit(t, seed, "checkout", "-b", "feat/shared")
	writeCommit(t, seed, "feature.txt", "feature\n", "feature")
	testutil.RunGit(t, seed, "push", "-u", "origin", "feat/shared")
	testutil.RunGit(t, seed, "checkout", "main")

	root := t.TempDir()
	project := config.Project{
		Remote:        remote,
		Path:          "personal/app",
		Status:        config.StatusActive,
		DefaultBranch: "main",
		Branches: []config.BranchMeta{{
			Name:     "feat/shared",
			Machines: []string{"archlinux"},
		}},
	}
	materializer := &materializer{machine: "macos", logf: func(string, ...any) {}}
	updated, err := materializer.materializeProject(context.Background(), root, "app", project)
	if err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "personal", "app")
	featurePath := layout.WorktreePath(mainPath, "macos", "feat/shared")
	if !git.IsRepo(mainPath) || !git.IsRepo(featurePath) {
		t.Fatalf("materialized repositories: main=%v feature=%v", git.IsRepo(mainPath), git.IsRepo(featurePath))
	}
	if updated == nil || !slices.Contains(updated.LookupBranch("feat/shared").Machines, "macos") {
		t.Fatalf("updated project = %#v", updated)
	}
	if branch, err := git.CurrentBranch(featurePath); err != nil || branch != "feat/shared" {
		t.Fatalf("feature branch = %q, error = %v", branch, err)
	}
}

func TestMaterializerFastForwardsCleanAndPreservesDirtyWorktree(t *testing.T) {
	useTestGitEnvironment(t)
	remote := testutil.InitFakeRemote(t, "app", "main")
	seed := filepath.Join(filepath.Dir(remote), "seed")
	root := t.TempDir()
	project := config.Project{Remote: remote, Path: "app", Status: config.StatusActive, DefaultBranch: "main"}
	materializer := &materializer{machine: "macos", logf: func(string, ...any) {}}
	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "app")
	before := git.RevParse(mainPath, "HEAD")

	writeCommit(t, seed, "remote.txt", "second\n", "second")
	testutil.RunGit(t, seed, "push", "origin", "main")
	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	after := git.RevParse(mainPath, "HEAD")
	if after == before || after != git.RevParse(seed, "HEAD") {
		t.Fatalf("clean worktree HEAD = %s, before = %s, remote = %s", after, before, git.RevParse(seed, "HEAD"))
	}

	testutil.AddDirty(t, mainPath)
	writeCommit(t, seed, "remote.txt", "third\n", "third")
	testutil.RunGit(t, seed, "push", "origin", "main")
	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	if got := git.RevParse(mainPath, "HEAD"); got != after {
		t.Fatalf("dirty worktree moved from %s to %s", after, got)
	}
	if !git.IsDirty(mainPath) {
		t.Fatal("dirty worktree was modified or cleaned")
	}
}

func TestMaterializerCreatesMissingMainWorktreeFromExistingBareStore(t *testing.T) {
	useTestGitEnvironment(t)
	remote := testutil.InitFakeRemote(t, "app", "main")
	root := t.TempDir()
	mainPath := filepath.Join(root, "app")
	testutil.CloneBare(t, remote, layout.BarePath(mainPath))
	project := config.Project{Remote: remote, Path: "app", Status: config.StatusActive, DefaultBranch: "main"}
	materializer := &materializer{machine: "macos", logf: func(string, ...any) {}}
	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	if !git.IsRepo(mainPath) {
		t.Fatal("main worktree was not created")
	}
	if branch, err := git.CurrentBranch(mainPath); err != nil || branch != "main" {
		t.Fatalf("main branch = %q, error = %v", branch, err)
	}
}

func TestMaterializerPreservesDivergedWorktree(t *testing.T) {
	useTestGitEnvironment(t)
	remote := testutil.InitFakeRemote(t, "app", "main")
	seed := filepath.Join(filepath.Dir(remote), "seed")
	root := t.TempDir()
	project := config.Project{Remote: remote, Path: "app", Status: config.StatusActive, DefaultBranch: "main"}
	materializer := &materializer{machine: "macos", logf: func(string, ...any) {}}
	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "app")
	writeCommit(t, mainPath, "local.txt", "local\n", "local")
	localHead := git.RevParse(mainPath, "HEAD")
	writeCommit(t, seed, "remote.txt", "remote\n", "remote")
	testutil.RunGit(t, seed, "push", "origin", "main")

	if _, err := materializer.materializeProject(context.Background(), root, "app", project); err != nil {
		t.Fatal(err)
	}
	if got := git.RevParse(mainPath, "HEAD"); got != localHead {
		t.Fatalf("diverged worktree moved from %s to %s", localHead, got)
	}
}

func TestMaterializerSkipsLockedProject(t *testing.T) {
	useTestGitEnvironment(t)
	root := t.TempDir()
	project := config.Project{Remote: testutil.InitFakeRemote(t, "app", "main"), Path: "app", Status: config.StatusActive, DefaultBranch: "main"}
	lock, err := repo.AcquireProjectLock(filepath.Join(root, "app"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	materializer := &materializer{machine: "macos", logf: func(string, ...any) {}}
	if _, err = materializer.materializeProject(context.Background(), root, "app", project); !errors.Is(err, repo.ErrProjectLocked) {
		t.Fatalf("materialize error = %v", err)
	}
	if git.IsRepo(filepath.Join(root, "app")) {
		t.Fatal("locked project was cloned")
	}
}

func useTestGitEnvironment(t *testing.T) {
	t.Helper()
	for _, value := range testutil.GitConfig() {
		name, setting, _ := strings.Cut(value, "=")
		t.Setenv(name, setting)
	}
}

func writeCommit(t *testing.T, repository, name, body, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, repository, "add", name)
	testutil.RunGit(t, repository, "commit", "-m", message)
}
