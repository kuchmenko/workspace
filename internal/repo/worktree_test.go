package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func setupWorktreeProject(t *testing.T, defaultBranch string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	remote := testutil.InitFakeRemote(t, "app", "main")
	mainPath := filepath.Join(root, "personal", "app")
	barePath := layout.BarePath(mainPath)
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, root, "clone", "--bare", remote, barePath)
	testutil.RunGit(t, barePath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	testutil.RunGit(t, barePath, "fetch", "origin")
	testutil.RunGit(t, barePath, "worktree", "add", mainPath, "main")
	workspace := &config.Workspace{
		Meta:   config.Meta{Version: 1, Root: root},
		Groups: map[string]config.Group{},
		Projects: map[string]config.Project{
			"app": {Path: filepath.Join("personal", "app"), Remote: remote, Status: config.StatusActive, DefaultBranch: defaultBranch},
		},
	}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	return root, mainPath, barePath
}

func addOptions(root, branch string) WorktreeAddOptions {
	return WorktreeAddOptions{WorkspaceRoot: root, Project: "app", Branch: branch, Machine: "linux"}
}

func TestAddWorktreeNewBranchFromDefault(t *testing.T) {
	root, _, _ := setupWorktreeProject(t, "main")
	result, err := AddWorktree(addOptions(root, "feat/new"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Base != "main" || result.Source != "" {
		t.Fatalf("result = %#v", result)
	}
	if branch := testutil.RunGit(t, result.Path, "branch", "--show-current"); branch != "feat/new" {
		t.Fatalf("branch = %q", branch)
	}
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	project := workspace.Projects["app"]
	if meta := project.LookupBranch("feat/new"); meta == nil || meta.CreatedBy != "linux" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestAddWorktreeAttachesRemoteAndMarksPushed(t *testing.T) {
	root, _, barePath := setupWorktreeProject(t, "main")
	remote := testutil.RunGit(t, barePath, "config", "remote.origin.url")
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.RunGit(t, t.TempDir(), "clone", remote, clone)
	testutil.RunGit(t, clone, "checkout", "-b", "feat/remote")
	testutil.RunGit(t, clone, "push", "origin", "feat/remote")

	result, err := AddWorktree(addOptions(root, "feat/remote"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != "fetched" {
		t.Fatalf("source = %q", result.Source)
	}
	if upstream := testutil.RunGit(t, result.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/feat/remote" {
		t.Fatalf("upstream = %q", upstream)
	}
	workspace, _ := config.Load(root)
	project := workspace.Projects["app"]
	meta := project.LookupBranch("feat/remote")
	if meta == nil || meta.LastPushedMachine != "linux" || meta.LastPushedAt == "" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestAddWorktreeReRegistersExistingWorktree(t *testing.T) {
	root, _, barePath := setupWorktreeProject(t, "main")
	existing := filepath.Join(root, "existing")
	testutil.RunGit(t, barePath, "worktree", "add", "-b", "feat/existing", existing, "main")
	result, err := AddWorktree(addOptions(root, "feat/existing"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReRegistered || result.Path != existing {
		t.Fatalf("result = %#v", result)
	}
	workspace, _ := config.Load(root)
	project := workspace.Projects["app"]
	if project.LookupBranch("feat/existing") == nil {
		t.Fatal("existing worktree was not registered")
	}
}

func TestAddWorktreeRequiresDefaultBase(t *testing.T) {
	root, _, _ := setupWorktreeProject(t, "")
	_, err := AddWorktree(addOptions(root, "feat/new"))
	if err == nil || !strings.Contains(err.Error(), "has no default_branch") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoveWorktreeRejectsDirty(t *testing.T) {
	root, _, _ := setupWorktreeProject(t, "main")
	result, err := AddWorktree(addOptions(root, "feat/dirty"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result.Path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = RemoveWorktree(WorktreeRemoveOptions{WorkspaceRoot: root, Project: "app", Branch: "feat/dirty", Machine: "linux"})
	if err == nil || !strings.Contains(err.Error(), "is dirty") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}
}

func TestRemoveWorktreeRejectsAhead(t *testing.T) {
	root, _, _ := setupWorktreeProject(t, "main")
	result, err := AddWorktree(addOptions(root, "feat/ahead"))
	if err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, result.Path, "push", "-u", "origin", "feat/ahead")
	if err := os.WriteFile(filepath.Join(result.Path, "ahead.txt"), []byte("ahead"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, result.Path, "add", "ahead.txt")
	testutil.RunGit(t, result.Path, "commit", "-m", "ahead")
	err = RemoveWorktree(WorktreeRemoveOptions{WorkspaceRoot: root, Project: "app", Branch: "feat/ahead", Machine: "linux"})
	if err == nil || !strings.Contains(err.Error(), "unpushed commits") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoveWorktreeForceReleasesRegistry(t *testing.T) {
	root, _, _ := setupWorktreeProject(t, "main")
	result, err := AddWorktree(addOptions(root, "feat/force"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result.Path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = RemoveWorktree(WorktreeRemoveOptions{WorkspaceRoot: root, Project: "app", Branch: "feat/force", Machine: "linux", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	workspace, _ := config.Load(root)
	project := workspace.Projects["app"]
	if project.LookupBranch("feat/force") != nil {
		t.Fatal("branch metadata was not released")
	}
}
