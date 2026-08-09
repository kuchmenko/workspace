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
				result := DeleteWorktreeDestructive(project, worktree, root)
				if result.Detail != "" || !strings.Contains(result.Message, "Deleted checkout") {
					t.Fatalf("delete = %+v", result)
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
	if m.lifecycle.phase != lifecycleReview || !strings.Contains(m.lifecycle.message, "uncommitted changes will be discarded") {
		t.Fatalf("dirty delete confirmation = phase %v message %q error %q", m.lifecycle.phase, m.lifecycle.message, m.lifecycle.errorText)
	}
	m.updateLifecycle(rune1('n'))
	if m.lifecycle != nil {
		t.Fatal("n did not cancel delete confirmation")
	}
	m.openWorktreeDelete(project, worktree)
	lm := m.lifecycle
	_, cmd := m.updateLifecycle(enter())
	if cmd == nil || m.lifecycle != nil || m.lifecycleJob != lm || lm.phase != lifecycleRunning {
		t.Fatalf("enter did not detach background delete: active %#v job %#v phase %v cmd %v", m.lifecycle, m.lifecycleJob, lm.phase, cmd)
	}
	result := runLifecycle(lm, nil, nil, func(string, string, bool) {})
	_, refresh := m.finishLifecycleJob(lifecycleDoneMsg{job: lm, result: result})
	if refresh != nil {
		m.Update(refresh())
	}
	if lm.phase != lifecycleResult || !strings.Contains(lm.message, "Deleted checkout") {
		t.Fatalf("enter did not execute delete: phase %v message %q error %q", lm.phase, lm.message, lm.errorText)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("confirmed dirty checkout still exists: %v", err)
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

			result := DeleteWorktreeDestructive(project, worktree, root)
			if result.Detail != "" || !strings.Contains(result.Message, "Deleted checkout") {
				t.Fatalf("delete %s = %+v", state, result)
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

func TestLifecycleJobDetachesAndReopensWithProgress(t *testing.T) {
	lm := &lifecycleModel{
		action:    lifecycleArchiveWorktree,
		phase:     lifecycleRunning,
		total:     3,
		startedAt: time.Now(),
		messages:  make(chan tui.Msg, 1),
	}
	m := &Model{lifecycle: lm, lifecycleJob: lm, mode: viewLifecycle, width: 90, height: 18}

	m.updateLifecycle(esc())
	if m.lifecycle != nil || m.lifecycleJob != lm || m.mode != viewList {
		t.Fatalf("detached lifecycle = active %#v job %#v mode %v", m.lifecycle, m.lifecycleJob, m.mode)
	}
	m.updateLifecycleProgress(lifecycleProgressMsg{job: lm, label: "feat/one", detail: "archived"})
	if lm.completed != 1 || !strings.Contains(m.lifecycleJobStatus(), "1/3") {
		t.Fatalf("detached progress = completed %d status %q", lm.completed, m.lifecycleJobStatus())
	}
	m.updateList(rune1('A'))
	if m.lifecycle != lm || m.mode != viewLifecycle {
		t.Fatalf("reopened lifecycle = active %#v mode %v", m.lifecycle, m.mode)
	}
}

func TestLifecycleConfirmationReturnsToOriginSheet(t *testing.T) {
	project := &Project{ID: "project", WorkspaceRoot: "/ws", Path: "/ws/project"}
	worktree := &Worktree{Path: "/ws/project-feature", Branch: "feat/background"}
	parent := &sheet{mode: sheetProject, target: project}
	lm := &lifecycleModel{
		scope:       lifecycleScope{kind: lifecycleWorktree, project: project, worktree: worktree, worktrees: []*Worktree{worktree}},
		action:      lifecycleArchiveWorktree,
		phase:       lifecycleReview,
		parentSheet: parent,
	}
	m := &Model{lifecycle: lm, mode: viewLifecycle, wtCache: NewWorktreeCache()}

	_, cmd := m.updateLifecycle(enter())
	if cmd == nil || m.lifecycle != nil || m.sheet != parent || m.mode != viewList || m.lifecycleJob != lm {
		t.Fatalf("confirmation did not return to sheet: active %#v sheet %#v mode %v job %#v", m.lifecycle, m.sheet, m.mode, m.lifecycleJob)
	}
}

func TestLifecycleAgePlanningRunsAsCommand(t *testing.T) {
	lm := &lifecycleModel{
		action: lifecycleArchiveOldWorktrees,
		phase:  lifecycleThreshold,
		input:  "72h",
		scope:  lifecycleScope{kind: lifecycleGlobal},
	}
	m := &Model{lifecycle: lm, mode: viewLifecycle}

	_, cmd := m.updateLifecycle(enter())
	if cmd == nil || lm.phase != lifecyclePlanning {
		t.Fatalf("planning = phase %v cmd %v", lm.phase, cmd)
	}
	m.Update(cmd())
	if lm.phase != lifecycleReview || lm.plan.Threshold != 72*time.Hour {
		t.Fatalf("finished planning = phase %v plan %+v", lm.phase, lm.plan)
	}
}

func TestArchiveProjectsReportsEveryTargetProgress(t *testing.T) {
	root := t.TempDir()
	ws := &config.Workspace{
		Meta: config.Meta{Version: 1},
		Projects: map[string]config.Project{
			"active":   {Path: "active", Status: config.StatusActive},
			"archived": {Path: "archived", Status: config.StatusArchived},
		},
		Groups: map[string]config.Group{},
	}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	projects := []worktreeCandidate{
		{WorkspaceRoot: root, ProjectID: "active"},
		{WorkspaceRoot: root, ProjectID: "archived"},
	}
	started, completed := 0, 0
	ArchiveProjects(projects, func(_ ProjectIdentity, start bool, _ string) {
		if start {
			started++
		} else {
			completed++
		}
	})
	if started != len(projects) || completed != len(projects) {
		t.Fatalf("progress = started %d completed %d, want %d each", started, completed, len(projects))
	}
}

func TestLifecycleJobBlocksForegroundRegistryMutation(t *testing.T) {
	lm := &lifecycleModel{phase: lifecycleRunning}
	project := &Project{Favorite: false}
	m := &Model{lifecycleJob: lm}

	m.toggleFavoriteFor(project)
	if project.Favorite || !strings.Contains(m.statusMsg, "unavailable") {
		t.Fatalf("favorite during lifecycle = favorite %v status %q", project.Favorite, m.statusMsg)
	}
}

func TestLifecycleRefreshClosesRemovedProjectSheet(t *testing.T) {
	project := Project{ID: "project", Name: "project", WorkspaceRoot: "/ws", Path: "/ws/project"}
	lm := &lifecycleModel{phase: lifecycleRefreshing}
	m := &Model{
		workspaces:   []WorkspaceData{{Root: "/ws", Projects: []Project{project}}},
		expanded:     map[string]bool{},
		wtCache:      NewWorktreeCache(),
		lifecycleJob: lm,
		mode:         viewList,
	}
	m.sheet = newProjectSheet(m, &m.workspaces[0].Projects[0], nil)
	lm.parentSheet = m.sheet

	m.finishLifecycleRefresh(lifecycleRefreshDoneMsg{
		job:        lm,
		workspaces: map[string]WorkspaceData{"/ws": {Root: "/ws"}},
	})
	if m.sheet != nil || lm.parentSheet != nil || lm.phase != lifecycleResult {
		t.Fatalf("refresh retained removed sheet: sheet %#v parent %#v phase %v", m.sheet, lm.parentSheet, lm.phase)
	}
}

func TestLifecycleRefreshClosesGlobalSearch(t *testing.T) {
	lm := &lifecycleModel{phase: lifecycleRefreshing}
	m := &Model{
		expanded:      map[string]bool{},
		wtCache:       NewWorktreeCache(),
		lifecycleJob:  lm,
		mode:          viewFlash,
		flashGlobal:   true,
		flashMatches:  []int{4},
		flashLabels:   []rune{'a'},
		savedItems:    []listItem{{kind: KindProject}},
		savedExpanded: map[string]bool{"old": true},
	}

	m.finishLifecycleRefresh(lifecycleRefreshDoneMsg{job: lm})
	if m.mode != viewList || m.flashGlobal || m.flashMatches != nil || m.savedItems != nil {
		t.Fatalf("refresh retained global search state: mode %v global %v matches %v saved %v", m.mode, m.flashGlobal, m.flashMatches, m.savedItems)
	}
}

func TestExplorerDebugLogRecordsLifecycleEvents(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	m := &Model{}
	if err := m.EnableDebugLog(); err != nil {
		t.Fatal(err)
	}
	path := m.DebugLogPath()
	m.logLifecycle("target=%s outcome=%s", "feat/one", "archived")
	if err := m.CloseDebugLog(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"explorer started", "target=feat/one outcome=archived", "explorer stopped"} {
		if !strings.Contains(string(contents), text) {
			t.Fatalf("debug log missing %q: %q", text, contents)
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
