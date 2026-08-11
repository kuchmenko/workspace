package agent

import (
	"fmt"
	"os"
	"os/exec"
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
			worktree.Dirty = true
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

func TestWorktreeLifecycleRejectsChangesAfterReview(t *testing.T) {
	t.Run("modified", func(t *testing.T) {
		root, project, reviewed, _ := lifecycleGitFixture(t)
		testutil.AddDirty(t, reviewed.Path)
		result := ArchiveWorktree(project, reviewed, root, true)
		if result.Err == nil || !strings.Contains(result.Err.Error(), "became modified") {
			t.Fatalf("archive = %+v", result)
		}
		if _, err := os.Stat(reviewed.Path); err != nil {
			t.Fatalf("checkout was mutated: %v", err)
		}
	})

	t.Run("head", func(t *testing.T) {
		root, project, reviewed, _ := lifecycleGitFixture(t)
		testutil.AddDirty(t, reviewed.Path)
		testutil.RunGit(t, reviewed.Path, "add", ".")
		testutil.RunGit(t, reviewed.Path, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "advance")
		result := DeleteWorktreeDestructive(project, reviewed, root)
		if !strings.Contains(result.Detail, "HEAD changed") {
			t.Fatalf("delete = %+v", result)
		}
		if _, err := os.Stat(reviewed.Path); err != nil {
			t.Fatalf("checkout was mutated: %v", err)
		}
	})
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
	_, cmd := m.updateLifecycle(enter())
	if cmd == nil || m.lifecycle != nil || len(m.jobs) != 1 {
		t.Fatalf("enter did not submit background delete: active %#v jobs %d cmd %v", m.lifecycle, len(m.jobs), cmd)
	}
	runExplorerJob(t, m, cmd)
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("confirmed dirty checkout still exists: %v", err)
	}
}

func TestBulkArchiveStillRejectsDirtyWorktree(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	testutil.AddDirty(t, worktree.Path)
	worktree.Dirty = true
	result := ArchiveWorktree(project, worktree, root, false)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "modified worktree") {
		t.Fatalf("bulk-safe archive = %+v", result)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("dirty checkout was removed: %v", err)
	}
}

func TestUnknownWorktreeIsNeverEligibleOrExecutable(t *testing.T) {
	p := &Project{ID: "p", Name: "p", Path: "/tmp/p"}
	unknown := Worktree{Path: "/tmp/p-wt", Branch: "feat/x", Unknown: true, LastActiveAt: time.Unix(1, 0)}
	plan := BuildWorktreeArchivePlan([]worktreeCandidate{{Project: p, Worktree: unknown}}, time.Hour, time.Unix(10000, 0), func(string) ([]Worktree, error) {
		return []Worktree{unknown}, nil
	})
	if len(plan.Eligible) != 0 {
		t.Fatalf("unknown worktree eligible: %+v", plan)
	}
	if err := validateWorktreeTarget(p, &unknown); err == nil {
		t.Fatal("unknown worktree validated as clean")
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
				worktree.HEAD = testutil.RunGit(t, worktree.Path, "rev-parse", "HEAD")
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

func TestDeleteReleasesOwnershipWhenLocalRefChangesAfterCheckoutRemoval(t *testing.T) {
	root, project, worktree, bare := lifecycleGitFixture(t)
	if err := os.WriteFile(filepath.Join(project.Path, "new-main.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, project.Path, "add", "new-main.txt")
	testutil.RunGit(t, project.Path, "commit", "-m", "new main")
	changedOID := testutil.RunGit(t, bare, "rev-parse", "main")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf("#!/bin/sh\n%s \"$@\"\nstatus=$?\ncase \" $* \" in *\" worktree remove \"*) if [ $status -eq 0 ]; then %s -C %s update-ref refs/heads/feat/lifecycle %s; fi;; esac\nexit $status\n", realGit, realGit, bare, changedOID)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	released := false
	result := deleteWorktreeDestructive(project, worktree, root, func(id, branch string) error {
		released = id == project.ID && branch == worktree.Branch
		return nil
	})
	if !released || !result.CheckoutRemoved || !result.MetadataReleased || result.BranchesDeleted {
		t.Fatalf("result = %+v, released = %t", result, released)
	}
	if !strings.Contains(result.Message, "branches remain") || !strings.Contains(result.Detail, "branches preserved") || !strings.Contains(result.Detail, "ownership metadata released") {
		t.Fatalf("result text = %+v", result)
	}
	if !gitRefExists(t, bare, "refs/heads/feat/lifecycle") || !gitRefExists(t, bare, "refs/remotes/origin/feat/lifecycle") {
		t.Fatal("branches were not preserved")
	}
}

func TestBulkArchiveRejectsCheckoutBranchChangeAfterReview(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	reviewed := *worktree
	testutil.RunGit(t, worktree.Path, "checkout", "-b", "feat/changed")

	_, reason, err := revalidateWorktreeArchiveCandidate(worktreeCandidate{
		WorkspaceRoot: root,
		ProjectID:     project.ID,
		Project:       project,
		Worktree:      reviewed,
	}, -time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason, "branch changed") {
		t.Fatalf("revalidation reason = %q", reason)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("checkout was mutated: %v", err)
	}
}

func TestWorktreeArchiveRevalidationClassifiesUnpublishedAsSkipped(t *testing.T) {
	root, project, worktree, _ := lifecycleGitFixture(t)
	testutil.RunGit(t, worktree.Path, "push", "origin", "--delete", worktree.Branch)
	result := ExecuteWorktreeArchivePlan(WorktreeArchivePlan{
		Threshold: -time.Hour,
		Eligible:  []worktreeCandidate{{WorkspaceRoot: root, ProjectID: project.ID, Project: project, Worktree: *worktree}},
	})
	if result.Skipped != 1 || result.Failed != 0 || !strings.Contains(strings.Join(result.Details, " "), "local-only") {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorktreeArchiveRevalidationClassifiesOperationalFailures(t *testing.T) {
	root, project, worktree, bare := lifecycleGitFixture(t)
	t.Run("registry", func(t *testing.T) {
		candidate := worktreeCandidate{WorkspaceRoot: filepath.Join(root, "missing"), ProjectID: project.ID, Project: project, Worktree: *worktree}
		result := ExecuteWorktreeArchivePlan(WorktreeArchivePlan{Threshold: -time.Hour, Eligible: []worktreeCandidate{candidate}})
		if result.Failed != 1 || result.Skipped != 0 || !strings.Contains(strings.Join(result.Details, " "), "registry reload failed") {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("transport", func(t *testing.T) {
		testutil.RunGit(t, bare, "remote", "set-url", "origin", filepath.Join(root, "unreachable.git"))
		candidate := worktreeCandidate{WorkspaceRoot: root, ProjectID: project.ID, Project: project, Worktree: *worktree}
		result := ExecuteWorktreeArchivePlan(WorktreeArchivePlan{Threshold: -time.Hour, Eligible: []worktreeCandidate{candidate}})
		if result.Failed != 1 || result.Skipped != 0 {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestAlreadyArchivedProjectOutcomeIsSkipped(t *testing.T) {
	kind, detail := lifecycleTargetOutcome(lifecycleArchiveProjects, lifecycleRunResult{})
	if kind != targetSkipped || !strings.Contains(detail, "already archived") {
		t.Fatalf("outcome = %s %q", kind, detail)
	}
}

func TestAsyncArchiveOldPreservesRevalidationClassification(t *testing.T) {
	for _, test := range []struct {
		name string
		kind targetOutcomeKind
	}{
		{name: "skip", kind: targetSkipped},
		{name: "failure", kind: targetFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind, _ := lifecycleTargetOutcome(lifecycleArchiveOldWorktrees, lifecycleRunResult{errorText: test.name, targetKind: test.kind})
			if kind != test.kind {
				t.Fatalf("outcome = %s, want %s", kind, test.kind)
			}
		})
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
	for _, text := range []string{"Lifecycle › all workspaces", "1 / a  Archive projects", "2 / w  Archive old worktrees", "1/a:archive projects", "q:back"} {
		if !strings.Contains(view, text) {
			t.Fatalf("lifecycle frame missing %q: %q", text, view)
		}
	}
}

func TestLifecycleJobDetachesAndReopensWithProgress(t *testing.T) {
	m := NewModel(nil)
	m.width, m.height = 90, 18
	m.submitJob("archive", 3, func(*jobContext) jobResult { return jobResult{} })
	m.updateList(rune1('A'))
	if m.mode != viewJobs {
		t.Fatalf("A opened mode %v", m.mode)
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
	if cmd == nil || m.lifecycle != nil || m.sheet != parent || m.mode != viewList || len(m.jobs) != 1 {
		t.Fatalf("confirmation did not return to sheet: active %#v sheet %#v mode %v jobs %d", m.lifecycle, m.sheet, m.mode, len(m.jobs))
	}
}

func TestLifecycleDeleteConfirmationSubmitsOnce(t *testing.T) {
	project := &Project{ID: "project", WorkspaceRoot: "/ws", Path: "/ws/project"}
	worktree := &Worktree{Path: "/ws/project-feature", Branch: "feat/delete"}
	m := &Model{mode: viewLifecycle, lifecycle: &lifecycleModel{
		scope:  lifecycleScope{kind: lifecycleWorktree, project: project, worktree: worktree, worktrees: []*Worktree{worktree}},
		action: lifecycleDeleteWorktree,
		phase:  lifecycleReview,
	}, wtCache: NewWorktreeCache()}
	_, cmd := m.updateLifecycle(enter())
	if cmd == nil || len(m.jobs) != 1 || m.lifecycle != nil {
		t.Fatalf("delete submission = cmd %v jobs %d lifecycle %#v", cmd, len(m.jobs), m.lifecycle)
	}
	m.Update(enter())
	if len(m.jobs) != 1 || m.mode == viewLifecycle {
		t.Fatalf("delete requested a second confirmation: jobs %d mode %v", len(m.jobs), m.mode)
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
	m := NewModel(nil)
	m.submitJob("lifecycle", 1, func(*jobContext) jobResult { return jobResult{} })
	if cmd := m.toggleFavoriteFor(&Project{}); cmd != nil {
		t.Fatal("unresolvable favorite submitted")
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

func TestLifecycleRefreshPreservesGlobalSearch(t *testing.T) {
	lm := &lifecycleModel{phase: lifecycleRefreshing}
	query := tui.NewTextInput()
	query.SetValue("query")
	m := &Model{
		expanded:      map[string]bool{},
		wtCache:       NewWorktreeCache(),
		lifecycleJob:  lm,
		mode:          viewFlash,
		flashGlobal:   true,
		flashEditing:  true,
		flashQuery:    query,
		flashMatches:  []int{4},
		flashLabels:   []rune{'a'},
		savedItems:    []listItem{{kind: KindProject}},
		savedExpanded: map[string]bool{"old": true},
	}

	m.finishLifecycleRefresh(lifecycleRefreshDoneMsg{job: lm})
	if m.mode != viewFlash || !m.flashGlobal || m.flashQuery.Value() != "query" {
		t.Fatalf("refresh lost global search state: mode %v global %v query %q saved %v", m.mode, m.flashGlobal, m.flashQuery.Value(), m.savedItems)
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
	project := &Project{ID: "project", Name: "project", WorkspaceRoot: root, Path: mainPath, DefaultBranch: "main"}
	worktrees, err := LoadWorktrees(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range worktrees {
		if worktrees[i].Path == worktreePath {
			return root, project, &worktrees[i], bare
		}
	}
	t.Fatal("feature worktree is missing")
	return "", nil, nil, ""
}

func gitRefExists(t *testing.T, repo, ref string) bool {
	t.Helper()
	return testutil.RunGitTry(t, repo, "show-ref", "--verify", "--quiet", ref) == nil
}
