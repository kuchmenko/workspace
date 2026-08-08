package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/testutil"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestParseArchiveThresholdUnitsAndOverflow(t *testing.T) {
	tests := map[string]time.Duration{
		"72h":    72 * time.Hour,
		"2d":     48 * time.Hour,
		"3w":     21 * 24 * time.Hour,
		"1month": 30 * 24 * time.Hour,
	}
	for input, want := range tests {
		got, err := ParseArchiveThreshold(input)
		if err != nil || got != want {
			t.Errorf("ParseArchiveThreshold(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	if _, err := ParseArchiveThreshold("9223372036854775807month"); err == nil {
		t.Fatal("overflow threshold was accepted")
	}
}

func TestLifecycleGroupScopeIsWorkspaceQualified(t *testing.T) {
	m := &Model{workspaces: []WorkspaceData{
		{Root: "/one", Projects: []Project{{ID: "one", WorkspaceRoot: "/one", Group: "shared"}}},
		{Root: "/two", Projects: []Project{{ID: "two", WorkspaceRoot: "/two", Group: "shared"}}},
	}}
	got := m.lifecycleProjects(lifecycleScope{kind: lifecycleGroup, workspaceRoot: "/two", group: "shared"})
	if len(got) != 1 || got[0].ProjectID != "two" {
		t.Fatalf("group scope = %#v", got)
	}
}

func TestBuildWorktreeArchivePlanClassifiesProtectedBranches(t *testing.T) {
	project := &Project{Path: filepath.Join(t.TempDir(), "project"), DefaultBranch: "trunk"}
	branches := []string{"main", "master", "dev", "live", "trunk"}
	plan := BuildWorktreeArchivePlan(
		[]worktreeCandidate{{Project: project}},
		24*time.Hour,
		time.Now(),
		func(string) ([]Worktree, error) {
			wts := make([]Worktree, len(branches))
			for i, branch := range branches {
				wts[i] = Worktree{Branch: branch}
			}
			return wts, nil
		},
	)
	if plan.Protected != len(branches) || len(plan.Eligible) != 0 || plan.Unpushed != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestArchiveAndDeleteWorktreeLifecycleWithRealGit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "test"}); err != nil {
		t.Fatal(err)
	}

	for _, destructive := range []bool{false, true} {
		t.Run(map[bool]string{false: "archive", true: "delete"}[destructive], func(t *testing.T) {
			root, project, worktree, bare := lifecycleGitFixture(t)
			testutil.AddDirty(t, worktree.Path)
			if destructive {
				message, detail := DeleteWorktreeDestructive(project, worktree, root)
				if detail != "" || !strings.Contains(message, "Deleted checkout") {
					t.Fatalf("delete = %q, %q", message, detail)
				}
				if gitRefExists(t, bare, "refs/heads/feat/lifecycle") || gitRefExists(t, bare, "refs/remotes/origin/feat/lifecycle") {
					t.Fatal("destructive delete preserved a branch")
				}
			} else {
				result := ArchiveWorktree(project, worktree, root, true)
				if result.Err != nil || !result.CheckoutRemoved || !result.MetadataReleased {
					t.Fatalf("archive = %+v", result)
				}
				if !gitRefExists(t, bare, "refs/heads/feat/lifecycle") || !gitRefExists(t, bare, "refs/remotes/origin/feat/lifecycle") {
					t.Fatal("archive removed a branch")
				}
			}
			if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
				t.Fatalf("checkout still exists: %v", err)
			}
		})
	}
}

func TestArchiveWorktreeRejectsCheckoutBranchMismatch(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	worktree.Branch = "feat/reviewed"
	result := ArchiveWorktree(project, worktree, root, true)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "branch changed") {
		t.Fatalf("archive = %+v", result)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("checkout was mutated: %v", err)
	}
}

func TestDirtySingleWorktreeActionsWarnButRemainConfirmable(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	testutil.AddDirty(t, worktree.Path)
	worktree.Dirty = true
	m := &Model{workspaces: []WorkspaceData{{Root: root, Projects: []Project{*project}}}, wtCache: NewWorktreeCache()}

	m.openWorktreeArchive(project, worktree)
	if m.lifecycle.phase != lifecycleReview || !strings.Contains(m.lifecycle.message, "uncommitted changes will be discarded") {
		t.Fatalf("dirty archive confirmation = phase %v message %q error %q", m.lifecycle.phase, m.lifecycle.message, m.lifecycle.errorText)
	}

	m.openWorktreeDelete(project, worktree)
	if m.lifecycle.phase != lifecycleTypedConfirm || !strings.Contains(m.lifecycle.message, "uncommitted changes will be discarded") {
		t.Fatalf("dirty delete confirmation = phase %v message %q error %q", m.lifecycle.phase, m.lifecycle.message, m.lifecycle.errorText)
	}
}

func TestBulkArchiveStillRejectsDirtyWorktree(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	testutil.AddDirty(t, worktree.Path)
	result := ArchiveWorktree(project, worktree, root, false)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "dirty worktree") {
		t.Fatalf("bulk-safe archive = %+v", result)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("dirty checkout was removed: %v", err)
	}
}

func TestDeleteDoesNotBlockAheadOrLocalOnlyWorktrees(t *testing.T) {
	for _, state := range []string{"ahead", "local-only"} {
		t.Run(state, func(t *testing.T) {
			root, project, worktree, bare := lifecycleGitFixture(t)
			switch state {
			case "ahead":
				if err := os.WriteFile(filepath.Join(worktree.Path, "ahead.txt"), []byte("ahead\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				testutil.RunGit(t, worktree.Path, "add", "ahead.txt")
				testutil.RunGit(t, worktree.Path, "commit", "-m", "ahead")
			case "local-only":
				testutil.RunGit(t, worktree.Path, "push", "origin", "--delete", worktree.Branch)
			}

			message, detail := DeleteWorktreeDestructive(project, worktree, root)
			if detail != "" || !strings.Contains(message, "Deleted checkout") {
				t.Fatalf("delete %s = %q, %q", state, message, detail)
			}
			if gitRefExists(t, bare, "refs/heads/"+worktree.Branch) {
				t.Fatalf("delete %s preserved local branch", state)
			}
		})
	}
}

func TestBulkArchiveRejectsCheckoutBranchChangeAfterReview(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	reviewed := *worktree
	testutil.RunGit(t, worktree.Path, "checkout", "-b", "feat/changed")

	_, reason := revalidateWorktreeArchiveCandidate(worktreeCandidate{
		WorkspaceRoot: root,
		ProjectID:     project.ID,
		Project:       project,
		Worktree:      reviewed,
	}, -time.Hour, time.Now())
	if !strings.Contains(reason, "branch changed") {
		t.Fatalf("revalidation reason = %q", reason)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("checkout was mutated: %v", err)
	}
}

func TestBulkArchiveReportsRemovedProjectPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "test"}); err != nil {
		t.Fatal(err)
	}
	root, project, worktree, _ := lifecycleGitFixture(t)
	result := ExecuteWorktreeArchivePlan(WorktreeArchivePlan{
		Threshold: -time.Hour,
		Eligible:  []worktreeCandidate{{WorkspaceRoot: root, ProjectID: project.ID, Project: project, Worktree: *worktree}},
	})
	if result.Archived != 1 || len(result.RemovedProjectPaths) != 1 || result.RemovedProjectPaths[0] != project.Path {
		t.Fatalf("result = %+v", result)
	}
	if len(result.AffectedProjects) != 1 || result.AffectedProjects[0] != (ProjectIdentity{root, project.ID}) {
		t.Fatalf("affected projects = %+v", result.AffectedProjects)
	}
	cache := NewWorktreeCache()
	cache.details[project.Path] = []Worktree{*worktree}
	cache.inventory[project.Path] = []Worktree{*worktree}
	for _, path := range result.RemovedProjectPaths {
		cache.Invalidate(path)
	}
	_, detailsOK := cache.details[project.Path]
	_, inventoryOK := cache.inventory[project.Path]
	if detailsOK || inventoryOK {
		t.Fatal("removed project path remained in worktree caches")
	}
}

func TestRemoveArchivedProjectsDoesNotRetargetProjectSheet(t *testing.T) {
	projects := []Project{{ID: "first", WorkspaceRoot: "/ws"}, {ID: "second", WorkspaceRoot: "/ws"}}
	m := &Model{workspaces: []WorkspaceData{{Root: "/ws", Projects: projects}}}
	s := &sheet{target: &m.workspaces[0].Projects[0]}
	m.removeArchivedProjects([]ProjectIdentity{{WorkspaceRoot: "/ws", ProjectID: "first"}})
	if s.target.ID != "first" || &m.workspaces[0].Projects[0] == s.target || m.workspaces[0].Projects[0].ID != "second" {
		t.Fatalf("sheet target=%s retained=%s", s.target.ID, m.workspaces[0].Projects[0].ID)
	}
}

func TestLifecycleUsesFullScreenFrameAndPersistentHints(t *testing.T) {
	m := &Model{
		width:     90,
		height:    18,
		lifecycle: &lifecycleModel{scope: lifecycleScope{kind: lifecycleGlobal}, phase: lifecycleSelect},
	}
	view := m.viewLifecycle()
	if tui.Height(view) != m.height {
		t.Fatalf("height = %d, want %d", tui.Height(view), m.height)
	}
	for _, text := range []string{"Lifecycle › all workspaces", "1 / a  Archive projects", "2 / w  Archive old worktrees", "1/a:archive projects", "esc:back"} {
		if !strings.Contains(view, text) {
			t.Fatalf("lifecycle frame missing %q: %q", text, view)
		}
	}
}

func lifecycleGitFixture(t *testing.T) (string, *Project, *Worktree, string) {
	t.Helper()
	root := t.TempDir()
	remote := testutil.InitFakeRemote(t, "remote", "main")
	mainPath := filepath.Join(root, "project")
	bare := layout.BarePath(mainPath)
	testutil.CloneBare(t, remote, bare)
	testutil.RunGit(t, bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	testutil.RunGit(t, bare, "fetch", "origin")
	testutil.RunGit(t, bare, "worktree", "add", mainPath, "main")
	worktreePath := filepath.Join(root, "project-feature")
	testutil.RunGit(t, bare, "worktree", "add", "-b", "feat/lifecycle", worktreePath, "main")
	testutil.RunGit(t, worktreePath, "push", "-u", "origin", "feat/lifecycle")
	ws := &config.Workspace{Meta: config.Meta{Version: 1}, Projects: map[string]config.Project{"project": {Path: "project", Status: config.StatusActive, DefaultBranch: "main"}}, Groups: map[string]config.Group{}}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	return root, &Project{ID: "project", Name: "project", WorkspaceRoot: root, Path: mainPath, DefaultBranch: "main"}, &Worktree{Path: worktreePath, Branch: "feat/lifecycle"}, bare
}

func gitRefExists(t *testing.T, repo, ref string) bool {
	t.Helper()
	return testutil.RunGitTry(t, repo, "show-ref", "--verify", "--quiet", ref) == nil
}
