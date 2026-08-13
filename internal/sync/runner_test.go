package sync

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestFrozenServiceBindingRejectsAuthorityChanges(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	legacyPlan := Plan{Root: root}
	if err := frozenServiceBinding(legacyPlan); err != nil {
		t.Fatal(err)
	}
	machine := &config.MachineConfig{Service: &config.MachineService{ID: "service", Endpoint: "https://service.local:47321", Bindings: []config.WorkspaceBinding{{Root: root, WorkspaceID: "workspace"}}}}
	if err := config.SaveMachineConfig(machine); err != nil {
		t.Fatal(err)
	}
	if err := frozenServiceBinding(legacyPlan); err == nil {
		t.Fatal("accepted service authority added after preflight")
	}
	servicePlan := Plan{Root: root, WorkspaceTargetID: "workspace:service", ServiceWorkspaceID: "workspace", Targets: []Target{{ID: "workspace:service", URL: "https://service.local:47321"}}}
	if err := frozenServiceBinding(servicePlan); err != nil {
		t.Fatal(err)
	}
	machine.Service.Endpoint = "https://other.local:47321"
	if err := config.SaveMachineConfig(machine); err != nil {
		t.Fatal(err)
	}
	if err := frozenServiceBinding(servicePlan); err == nil {
		t.Fatal("accepted service endpoint changed after preflight")
	}
}

func TestRunContextLeavesExcludedExistingAndMissingProjectsUntouched(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "projects", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"a-existing": activeProject(remote, "personal/existing"),
		"b-missing":  activeProject(remote, "personal/missing"),
		"c-selected": activeProject(remote, "personal/selected"),
	}}
	saveTestWorkspace(t, root, workspace)
	existingBare := layout.BarePath(filepath.Join(root, "personal", "existing"))
	if err := os.MkdirAll(filepath.Dir(existingBare), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, remote, existingBare)

	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	selection.ExcludeProject("a-existing")
	selection.ExcludeProject("b-missing")
	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)

	if git.HasFetchRefspec(existingBare) {
		t.Fatal("excluded existing project was modified")
	}
	if _, err := os.Stat(filepath.Join(root, "personal", "missing")); !os.IsNotExist(err) {
		t.Fatalf("excluded missing project was created: %v", err)
	}
	if !git.IsRepo(filepath.Join(root, "personal", "selected")) {
		t.Fatal("selected project was not cloned")
	}
	if got := projectResultStatuses(report.Projects); !equalStrings(got, []string{"a-existing:skipped", "b-missing:skipped", "c-selected:success"}) {
		t.Fatalf("project results = %v", got)
	}
}

func TestRunContextLeavesExcludedMirrorUntouched(t *testing.T) {
	root := newTestWorkspace(t)
	origin := testutil.InitFakeRemote(t, "origin", "main")
	mirror := testutil.InitFakeRemote(t, "mirror", "main")
	project := activeProject(origin, "personal/project")
	project.Mirrors = map[string]string{"backup": mirror}
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": project}}
	saveTestWorkspace(t, root, workspace)
	barePath := layout.BarePath(filepath.Join(root, project.Path))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, origin, barePath)

	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	selection.ExcludeTarget(plan.Projects[0].MirrorIDs[0])
	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	remotes, err := git.ListRemotes(barePath)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(remotes, []string{"origin"}) {
		t.Fatalf("remotes = %v, excluded mirror was installed", remotes)
	}
	if len(report.Mirrors) != 1 || report.Mirrors[0].Status != ResultSkipped {
		t.Fatalf("mirror results = %+v", report.Mirrors)
	}
}

func TestRunContextMissingProjectPushesOnlySelectedMirrors(t *testing.T) {
	root := newTestWorkspace(t)
	origin := testutil.InitFakeRemote(t, "clone-origin", "main")
	selectedMirror := filepath.Join(t.TempDir(), "selected.git")
	excludedMirror := filepath.Join(t.TempDir(), "excluded.git")
	testutil.RunGit(t, filepath.Dir(selectedMirror), "init", "--bare", selectedMirror)
	testutil.RunGit(t, filepath.Dir(excludedMirror), "init", "--bare", excludedMirror)
	project := activeProject(origin, "personal/project")
	project.Mirrors = map[string]string{"excluded": excludedMirror, "selected": selectedMirror}
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": project}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	selection.ExcludeTarget(plan.Projects[0].MirrorIDs[0])

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	barePath := layout.BarePath(filepath.Join(root, project.Path))
	remotes, err := git.ListRemotes(barePath)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(remotes, []string{"origin", "selected"}) {
		t.Fatalf("installed remotes = %v", remotes)
	}
	if git.RevParse(selectedMirror, "refs/heads/main") == "" {
		t.Fatal("selected mirror did not receive main")
	}
	if git.RevParse(excludedMirror, "refs/heads/main") != "" {
		t.Fatal("excluded mirror was contacted")
	}
	if len(report.Mirrors) != 2 || report.Mirrors[0].Mirror != "excluded" || report.Mirrors[0].Status != ResultSkipped || report.Mirrors[1].Mirror != "selected" || report.Mirrors[1].Status != ResultSuccess {
		t.Fatalf("mirror results = %+v", report.Mirrors)
	}
}

func TestRunContextSkipsProjectWhenBareOriginChangedAfterPreflight(t *testing.T) {
	root := newTestWorkspace(t)
	origin := testutil.InitFakeRemote(t, "planned-origin", "main")
	project := activeProject(origin, "personal/project")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": project}}
	saveTestWorkspace(t, root, workspace)
	barePath := layout.BarePath(filepath.Join(root, project.Path))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, origin, barePath)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	changedOrigin := filepath.Join(t.TempDir(), "missing.git")
	if err := git.SetRemoteURL(barePath, changedOrigin); err != nil {
		t.Fatal(err)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSkipped || report.Projects[0].Reason != SkipPlanChanged {
		t.Fatalf("project results = %+v", report.Projects)
	}
	if !strings.Contains(report.Projects[0].Diagnostic, changedOrigin) {
		t.Fatalf("project diagnostic = %q", report.Projects[0].Diagnostic)
	}
}

func TestRunContextFetchesOnlyDeclaredProjectOrigin(t *testing.T) {
	root := newTestWorkspace(t)
	origin := testutil.InitFakeRemote(t, "origin-only", "main")
	project := activeProject(origin, "personal/project")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": project}}
	saveTestWorkspace(t, root, workspace)
	barePath := layout.BarePath(filepath.Join(root, project.Path))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, origin, barePath)
	testutil.RunGit(t, barePath, "remote", "add", "unplanned", filepath.Join(t.TempDir(), "missing.git"))
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("project results = %+v", report.Projects)
	}
}

func TestRunContextSkipsWorkspaceWithoutPlannedOrigin(t *testing.T) {
	root := newTestWorkspace(t)
	testutil.RunGit(t, root, "init", "--initial-branch=main")
	workspace := &config.Workspace{Projects: map[string]config.Project{}}
	saveTestWorkspace(t, root, workspace)
	testutil.RunGit(t, root, "add", "workspace.toml")
	testutil.RunGit(t, root, "commit", "-m", "workspace")
	testutil.RunGit(t, root, "remote", "add", "backup", filepath.Join(t.TempDir(), "missing.git"))
	plan := BuildPlan(root, workspace)
	if plan.WorkspaceTargetID != "" {
		t.Fatalf("workspace target = %q", plan.WorkspaceTargetID)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Workspace) != 1 || report.Workspace[0].Status != ResultSkipped || report.Workspace[0].Reason != SkipState {
		t.Fatalf("workspace results = %+v", report.Workspace)
	}
}

func TestRunContextSkipsPlanChangedProject(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "original", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(remote, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))

	changed := workspace.Projects["project"]
	changed.DefaultBranch = "trunk"
	workspace.Projects["project"] = changed
	workspace.Projects["new-project"] = activeProject(remote, "personal/new-project")
	saveTestWorkspace(t, root, workspace)
	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Projects) != 2 || report.Projects[0].Reason != SkipPlanChanged || report.Projects[1].Reason != SkipPlanChanged {
		t.Fatalf("project results = %+v", report.Projects)
	}
	if _, err := os.Stat(filepath.Join(root, "personal", "project")); !os.IsNotExist(err) {
		t.Fatalf("plan-changed project was cloned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "personal", "new-project")); !os.IsNotExist(err) {
		t.Fatalf("new unprobed project was cloned: %v", err)
	}
}

func TestRunContextCancellationStartsNoProject(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "project", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(remote, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := newTestRunner(t, root).RunContext(ctx, selection, nil)
	if !report.Canceled {
		t.Fatalf("report = %+v, want canceled", report)
	}
	if _, err := os.Stat(filepath.Join(root, "personal", "project")); !os.IsNotExist(err) {
		t.Fatalf("project started after cancellation: %v", err)
	}
}

func TestRunContextCancellationStartsNoFollowingProject(t *testing.T) {
	root := newTestWorkspace(t)
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"alpha": activeProject(testutil.InitFakeRemote(t, "alpha-cancel", "main"), "alpha"),
		"omega": activeProject(testutil.InitFakeRemote(t, "omega-cancel", "main"), "omega"),
	}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	ctx, cancel := context.WithCancel(context.Background())
	report := newTestRunner(t, root).RunContext(ctx, selection, func(event Event) {
		if event.Kind == EventProject && event.Project == "alpha" {
			cancel()
		}
	})
	if !report.Canceled {
		t.Fatalf("report = %+v, want canceled", report)
	}
	if !git.IsRepo(filepath.Join(root, "alpha")) {
		t.Fatal("first project did not complete")
	}
	if _, err := os.Stat(filepath.Join(root, "omega")); !os.IsNotExist(err) {
		t.Fatalf("following project started after cancellation: %v", err)
	}
}

func TestRunContextProjectAndEventOrder(t *testing.T) {
	root := newTestWorkspace(t)
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"zeta":  activeProject(testutil.InitFakeRemote(t, "zeta", "main"), "zeta"),
		"alpha": activeProject(testutil.InitFakeRemote(t, "alpha", "main"), "alpha"),
	}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	var events []Event
	report := newTestRunner(t, root).RunContext(context.Background(), selection, func(event Event) {
		events = append(events, event)
	})
	if got := projectResultStatuses(report.Projects); !equalStrings(got, []string{"alpha:success", "zeta:success"}) {
		t.Fatalf("project results = %v", got)
	}
	var projectEvents []string
	for _, event := range events {
		if event.Kind == EventProject {
			projectEvents = append(projectEvents, event.Project)
		}
	}
	if !equalStrings(projectEvents, []string{"alpha", "zeta"}) {
		t.Fatalf("project events = %v", projectEvents)
	}
}

func TestRunContextEmitsProjectStartBeforeCompletion(t *testing.T) {
	root := newTestWorkspace(t)
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"app": activeProject(testutil.InitFakeRemote(t, "app", "main"), "app"),
	}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	var events []Event
	newTestRunner(t, root).RunContext(context.Background(), selection, func(event Event) {
		events = append(events, event)
	})
	started, completed := -1, -1
	for index, event := range events {
		if event.Project != "app" {
			continue
		}
		if event.Kind == EventStarted && event.Operation == "project-sync" && started == -1 {
			started = index
		}
		if event.Kind == EventProject {
			completed = index
		}
	}
	if started == -1 || completed == -1 || started >= completed {
		t.Fatalf("project event positions: start=%d complete=%d events=%v", started, completed, events)
	}
}

func newTestWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return t.TempDir()
}

func saveTestWorkspace(t *testing.T, root string, workspace *config.Workspace) {
	t.Helper()
	if workspace.Groups == nil {
		workspace.Groups = map[string]config.Group{}
	}
	if workspace.Aliases == nil {
		workspace.Aliases = map[string]string{}
	}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
}

func newTestRunner(t *testing.T, root string) *Runner {
	t.Helper()
	return NewRunner(root, log.New(io.Discard, "", 0))
}

func projectResultStatuses(results []OperationResult) []string {
	values := make([]string, len(results))
	for index, result := range results {
		values[index] = result.Project + ":" + string(result.Status)
	}
	return values
}
