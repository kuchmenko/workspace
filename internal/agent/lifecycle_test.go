package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
	"codeberg.org/kuchmenko/workspace/internal/tui"
)

func TestParseArchiveThreshold(t *testing.T) {
	for input, want := range map[string]time.Duration{"72h": 72 * time.Hour, "1w": 7 * 24 * time.Hour, "2d": 48 * time.Hour, "1month": 30 * 24 * time.Hour} {
		got, err := ParseArchiveThreshold(input)
		if err != nil || got != want {
			t.Errorf("ParseArchiveThreshold(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0h", "-1d", "1m", "week"} {
		if _, err := ParseArchiveThreshold(input); err == nil {
			t.Errorf("ParseArchiveThreshold(%q) accepted invalid input", input)
		}
	}
	if _, err := ParseArchiveThreshold("999999999999999999h"); err == nil {
		t.Fatal("overflowing threshold was accepted")
	}
}

func TestArchiveProjectScopes(t *testing.T) {
	root := t.TempDir()
	ws := &config.Workspace{Projects: map[string]config.Project{
		"one":   {Status: config.StatusActive, Group: "g"},
		"two":   {Status: config.StatusActive, Group: "g"},
		"three": {Status: config.StatusActive, Group: "other"},
	}}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	projects := []worktreeCandidate{{WorkspaceRoot: root, ProjectID: "one"}, {WorkspaceRoot: root, ProjectID: "two"}}
	result := ArchiveProjects(projects)
	if len(result.Failures) != 0 || len(result.Succeeded) != 2 {
		t.Fatalf("ArchiveProjects = %+v", result)
	}
	got, _ := config.Load(root)
	if got.Projects["one"].Status != config.StatusArchived || got.Projects["two"].Status != config.StatusArchived || got.Projects["three"].Status != config.StatusActive {
		t.Fatalf("unexpected statuses: %+v", got.Projects)
	}
}

func TestArchiveProjectsKeepsSuccessWhenLaterRootFails(t *testing.T) {
	base := t.TempDir()
	first, second := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, root := range []string{first, second} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := config.Save(root, &config.Workspace{Projects: map[string]config.Project{"p": {Status: config.StatusActive}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(second, 0o555); err != nil {
		t.Fatal(err)
	}
	secondFile := filepath.Join(second, "workspace.toml")
	if err := os.Chmod(secondFile, 0o444); err != nil {
		t.Fatal(err)
	}
	defer func() { os.Chmod(secondFile, 0o644); os.Chmod(second, 0o755) }()
	result := ArchiveProjects([]worktreeCandidate{{WorkspaceRoot: second, ProjectID: "p"}, {WorkspaceRoot: first, ProjectID: "p"}})
	if len(result.Succeeded) != 1 || result.Succeeded[0].WorkspaceRoot != first || len(result.Failures) != 1 {
		t.Fatalf("partial result = %+v", result)
	}
	loaded, err := config.Load(first)
	if err != nil || loaded.Projects["p"].Status != config.StatusArchived {
		t.Fatalf("first root was not persisted: %v, %+v", err, loaded)
	}
}

func TestArchiveWorktreePreservesBranchesAndDeleteRemovesThem(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, project, first := lifecycleFixture(t, "feat/archive")
	if result := ArchiveWorktree(project, first, root); result.Err != nil {
		t.Fatal(result.Err)
	}
	bare := filepath.Join(root, "proj.bare")
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("archived checkout still exists: %v", err)
	}
	if !git.HasBranch(bare, first.Branch) || !git.HasRemoteBranch(bare, "origin", first.Branch) {
		t.Fatal("archive removed a branch")
	}

	_, project, second := lifecycleFixture(t, "feat/delete")
	message, failure := DeleteWorktreeDestructive(project, second, filepath.Dir(project.Path))
	if failure != "" {
		t.Fatalf("delete failed: %s (%s)", failure, message)
	}
	bare = filepath.Join(filepath.Dir(project.Path), "proj.bare")
	if git.HasBranch(bare, second.Branch) || git.HasRemoteBranch(bare, "origin", second.Branch) {
		t.Fatal("destructive delete retained local or remote branch")
	}
}

func TestArchiveWorktreeReportsCheckoutRemovedWhenMetadataFails(t *testing.T) {
	root, project, wt := lifecycleFixture(t, "feat/partial")
	path, err := config.MachineConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	result := ArchiveWorktree(project, wt, root)
	if !result.CheckoutRemoved || result.MetadataReleased || result.Err == nil {
		t.Fatalf("archive partial result = %+v", result)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("checkout remains after partial archive: %v", err)
	}
}

func TestLifecycleRejectsProtectedDirtyAndUnpushed(t *testing.T) {
	root, project, wt := lifecycleFixture(t, "feat/check")
	_ = root
	project.DefaultBranch = wt.Branch
	if err := validateDeleteWorktree(project, wt); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protected branch error = %v", err)
	}
	project.DefaultBranch = "main"
	if err := os.WriteFile(filepath.Join(wt.Path, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateDeleteWorktree(project, wt); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty worktree error = %v", err)
	}
	os.Remove(filepath.Join(wt.Path, "dirty"))
	if err := os.WriteFile(filepath.Join(wt.Path, "ahead"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, wt.Path, "add", "ahead")
	testutil.RunGit(t, wt.Path, "commit", "-m", "ahead")
	if err := validateDeleteWorktree(project, wt); err == nil || !strings.Contains(err.Error(), "unpushed") {
		t.Fatalf("unpushed worktree error = %v", err)
	}
}

func TestBulkPlannerClassifiesWorktrees(t *testing.T) {
	root, project, published := lifecycleFixture(t, "feat/old")
	bare := filepath.Join(root, "proj.bare")
	localPath := filepath.Join(root, "proj-wt-local")
	if err := git.WorktreeAdd(bare, localPath, "feat/local", "main"); err != nil {
		t.Fatal(err)
	}
	project.BranchActivity = map[string]time.Time{"feat/old": time.Now().Add(-40 * 24 * time.Hour)}
	plan := BuildWorktreeArchivePlan([]worktreeCandidate{{WorkspaceRoot: root, ProjectID: project.ID, Project: project}}, 30*24*time.Hour, time.Now(), func(string) []Worktree {
		return []Worktree{
			{Path: project.Path, Branch: "main", IsMain: true},
			{Path: published.Path, Branch: published.Branch, LastActiveAt: time.Now().Add(-60 * 24 * time.Hour)},
			{Path: localPath, Branch: "feat/local", LastActiveAt: time.Now().Add(-60 * 24 * time.Hour)},
			{Path: "dirty", Branch: "feat/dirty", Dirty: true},
			{Path: "recent", Branch: published.Branch, LastActiveAt: time.Now()},
		}
	})
	if plan.Considered != 5 || len(plan.Eligible) != 1 || plan.Main != 1 || plan.Dirty != 1 || plan.Unpushed != 1 || plan.Recent != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestBulkExecutionSkipsCandidateThatBecomesRecentAfterPreview(t *testing.T) {
	root, project, wt := lifecycleFixture(t, "feat/recent")
	old := time.Now().Add(-60 * 24 * time.Hour)
	plan := BuildWorktreeArchivePlan([]worktreeCandidate{{WorkspaceRoot: root, ProjectID: project.ID, Project: project}}, 30*24*time.Hour, time.Now(), func(string) []Worktree {
		return []Worktree{{Path: wt.Path, Branch: wt.Branch, LastActiveAt: old}}
	})
	if len(plan.Eligible) != 1 {
		t.Fatalf("preview plan = %+v", plan)
	}
	ws, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	stored := ws.Projects[project.ID]
	stored.Branches[0].LastActiveAt = time.Now().UTC().Format(time.RFC3339)
	ws.Projects[project.ID] = stored
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}

	result := ExecuteWorktreeArchivePlan(plan)
	if result.Archived != 0 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("execution result = %+v", result)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("recent checkout was archived: %v", err)
	}
}

func TestDeleteReloadsLiveDefaultBranchBeforeMutation(t *testing.T) {
	root, project, wt := lifecycleFixture(t, "feat/protected-live")
	ws, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	stored := ws.Projects[project.ID]
	stored.DefaultBranch = wt.Branch
	ws.Projects[project.ID] = stored
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	message, failure := DeleteWorktreeDestructive(project, wt, root)
	if !strings.Contains(failure, "protected") || message != "checkout unchanged" {
		t.Fatalf("delete result = %q, %q", message, failure)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("protected checkout was removed: %v", err)
	}
}

func TestSingleArchiveFailureReportsCheckoutUnchanged(t *testing.T) {
	project := &Project{ID: "p", Name: "p", Path: t.TempDir()}
	wt := &Worktree{Path: filepath.Join(t.TempDir(), "missing"), Branch: "feat/missing"}
	m := NewModel([]WorkspaceData{{Root: filepath.Dir(project.Path), Projects: []Project{*project}}}, nil)
	m.lifecycle = &lifecycleModel{scope: lifecycleScope{kind: lifecycleWorktree, project: project, worktree: wt}, action: lifecycleArchiveWorktree}
	m.executeLifecycle()
	if m.lifecycle.message != "Checkout unchanged." || m.lifecycle.errorText == "" {
		t.Fatalf("archive result = %q, %q", m.lifecycle.message, m.lifecycle.errorText)
	}
}

func TestLifecycleReducerScopeAndTypedConfirmation(t *testing.T) {
	p := &Project{Name: "one", BranchActivity: map[string]time.Time{}}
	m := NewModel([]WorkspaceData{{Projects: []Project{*p}}}, nil)
	m.openLifecycle(lifecycleScope{kind: lifecycleGlobal})
	m.updateLifecycle(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'a'}})
	if m.lifecycle.action != lifecycleArchiveProjects || m.lifecycle.phase != lifecycleReview || !strings.Contains(m.lifecycle.message, "1 project") {
		t.Fatalf("global archive review = %+v", m.lifecycle)
	}
	m.lifecycle = &lifecycleModel{scope: lifecycleScope{worktree: &Worktree{Branch: "feat/exact"}}, phase: lifecycleTypedConfirm}
	m.mode = viewLifecycle
	for _, r := range "wrong" {
		m.updateLifecycle(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{r}})
	}
	m.updateLifecycle(tui.KeyMsg{Type: tui.KeyEnter})
	if !strings.Contains(m.lifecycle.errorText, "exactly match") {
		t.Fatalf("typed confirmation accepted mismatch: %+v", m.lifecycle)
	}
}

func TestGroupScopeUsesWorkspaceRootAndPreservesEmptyGroup(t *testing.T) {
	p1 := Project{ID: "one", Group: "Go", Path: "/one/p"}
	p2 := Project{ID: "two", Group: "Go", Path: "/two/p"}
	m := projectionTestModel(nil)
	m.workspaces = []WorkspaceData{
		{Root: "/one", Groups: []string{"Go", "empty"}, Projects: []Project{p1}},
		{Root: "/two", Groups: []string{"Go"}, Projects: []Project{p2}},
	}
	got := m.lifecycleProjects(lifecycleScope{kind: lifecycleGroup, group: "Go", workspaceRoot: "/two"})
	if len(got) != 1 || got[0].ProjectID != "two" {
		t.Fatalf("group scope = %+v", got)
	}
	m.removeArchivedProjects([]ProjectIdentity{{WorkspaceRoot: "/one", ProjectID: "one"}})
	if len(m.workspaces[0].Groups) != 2 || m.workspaces[0].Groups[1] != "empty" {
		t.Fatalf("configured groups were discarded: %v", m.workspaces[0].Groups)
	}
}

func TestLanguageGroupCannotOpenCanonicalGroupSheet(t *testing.T) {
	m := projectionTestModel([]Project{{ID: "one", Name: "one", Group: "Go", Language: "Go"}})
	m.workspaces[0].Groups = []string{"Go"}
	m.homeView = config.ExplorerViewLanguage
	m.expanded["lang:Go"] = true
	m.rebuildItems()
	m.cursor = 0
	m.updateList(tui.KeyMsg{Type: tui.KeyEnter})
	if m.sheet != nil || m.items[0].groupKind != groupLanguage {
		t.Fatal("language heading opened canonical group sheet")
	}
}

func TestGlobalSearchWorktreeActivationQuits(t *testing.T) {
	p := Project{Name: "one", Path: "/ws/one"}
	m := projectionTestModel([]Project{p})
	m.wtCache.data[p.Path] = []Worktree{{Path: "/ws/one-wt", Branch: "feat/x"}}
	m.flashGlobal, m.mode, m.flashQuery = true, viewFlash, "feat/x"
	m.recomputeFlash()
	_, cmd := m.updateFlash(tui.KeyMsg{Type: tui.KeyEnter})
	if cmd == nil {
		t.Fatal("worktree activation did not quit")
	}
	if _, ok := cmd().(tui.QuitMsg); !ok {
		t.Fatalf("activation command = %T", cmd())
	}
}

func lifecycleFixture(t *testing.T, branch string) (string, *Project, *Worktree) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "unknown"}); err != nil {
		t.Fatal(err)
	}
	remote := testutil.InitFakeRemote(t, "remote", "main")
	root := t.TempDir()
	bare := filepath.Join(root, "proj.bare")
	mainPath := filepath.Join(root, "proj")
	testutil.RunGit(t, root, "clone", "--bare", remote, bare)
	if err := git.SetFetchRefspec(bare); err != nil {
		t.Fatal(err)
	}
	if err := git.Fetch(bare); err != nil {
		t.Fatal(err)
	}
	if err := git.WorktreeAdd(bare, mainPath, "main", ""); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other")
	testutil.RunGit(t, root, "clone", remote, other)
	testutil.RunGit(t, other, "checkout", "-b", branch)
	testutil.RunGit(t, other, "push", "-u", "origin", branch)
	if err := git.Fetch(bare); err != nil {
		t.Fatal(err)
	}
	if err := git.FetchRefspec(bare, "origin", "+refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(root, "proj-wt-"+strings.ReplaceAll(branch, "/", "-"))
	if err := git.WorktreeAdd(bare, wtPath, branch, ""); err != nil {
		t.Fatal(err)
	}
	project := &Project{ID: "proj", Name: "proj", Path: mainPath, DefaultBranch: "main", BranchActivity: map[string]time.Time{}}
	ws := &config.Workspace{Projects: map[string]config.Project{"proj": {Path: "proj", Status: config.StatusActive, DefaultBranch: "main", Branches: []config.BranchMeta{{Name: branch, Machines: []string{"unknown"}}}}}}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	return root, project, &Worktree{Path: wtPath, Branch: branch}
}
