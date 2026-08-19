package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestRunContextPublishesChangedLocalOrigin(t *testing.T) {
	root := newTestWorkspace(t)
	declared := testutil.InitFakeRemote(t, "declared", "main")
	changed := testutil.InitFakeRemote(t, "changed", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(declared, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	barePath := cloneTestProject(t, root, workspace.Projects["project"])
	if err := git.SetRemoteURL(barePath, changed); err != nil {
		t.Fatal(err)
	}

	baselines := map[string]string{"project": declared}
	saveTestOriginBaselines(t, root, baselines)
	plan := BuildPlanWithBaselines(root, workspace, baselines)
	if plan.Projects[0].OriginURL != changed {
		t.Fatalf("planned origin = %q, want local %q", plan.Projects[0].OriginURL, changed)
	}
	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if got := loadTestWorkspace(t, root).Projects["project"].Remote; got != changed {
		t.Fatalf("registry remote = %q, want %q", got, changed)
	}
	if got := loadTestOriginBaselines(t, root)["project"]; got != changed {
		t.Fatalf("origin baseline = %q, want %q", got, changed)
	}
}

func TestRunContextAppliesIncomingOriginToExistingCheckout(t *testing.T) {
	root := newTestWorkspace(t)
	declared := testutil.InitFakeRemote(t, "declared", "main")
	incoming := testutil.InitFakeRemote(t, "incoming", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(declared, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	barePath := cloneTestProject(t, root, workspace.Projects["project"])
	baselines := map[string]string{"project": declared}
	saveTestOriginBaselines(t, root, baselines)
	before := BuildPlanWithBaselines(root, workspace, baselines)
	project := workspace.Projects["project"]
	project.Remote = incoming
	workspace.Projects["project"] = project
	saveTestWorkspace(t, root, workspace)

	plan := RefreshPlan(root, workspace, before)
	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if got, err := git.ConfiguredRemoteURL(barePath, "origin"); err != nil || got != incoming {
		t.Fatalf("local origin = %q, %v; want %q", got, err, incoming)
	}
	if got := loadTestOriginBaselines(t, root)["project"]; got != incoming {
		t.Fatalf("origin baseline = %q, want %q", got, incoming)
	}
}

func TestRunContextRecordsConcurrentOriginChanges(t *testing.T) {
	root := newTestWorkspace(t)
	declared := testutil.InitFakeRemote(t, "declared", "main")
	local := testutil.InitFakeRemote(t, "local", "main")
	incoming := testutil.InitFakeRemote(t, "incoming", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(declared, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	barePath := cloneTestProject(t, root, workspace.Projects["project"])
	baselines := map[string]string{"project": declared}
	saveTestOriginBaselines(t, root, baselines)
	before := BuildPlanWithBaselines(root, workspace, baselines)
	if err := git.SetRemoteURL(barePath, local); err != nil {
		t.Fatal(err)
	}
	project := workspace.Projects["project"]
	project.Remote = incoming
	workspace.Projects["project"] = project
	saveTestWorkspace(t, root, workspace)

	plan := RefreshPlan(root, workspace, before)
	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Conflicts) != 1 || report.Conflicts[0].Operation != string(conflict.KindOriginDivergence) {
		t.Fatalf("conflicts = %+v", report.Conflicts)
	}
	if got, _ := git.ConfiguredRemoteURL(barePath, "origin"); got != local {
		t.Fatalf("local origin = %q, want unchanged %q", got, local)
	}
	if got := loadTestWorkspace(t, root).Projects["project"].Remote; got != incoming {
		t.Fatalf("registry remote = %q, want unchanged %q", got, incoming)
	}
	if got := loadTestOriginBaselines(t, root)["project"]; got != declared {
		t.Fatalf("origin baseline = %q, want unchanged %q", got, declared)
	}
}

func TestRunContextInitializesMatchingOriginBaseline(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "declared", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(remote, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	cloneTestProject(t, root, workspace.Projects["project"])
	plan := BuildPlan(root, workspace)

	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if got := loadTestOriginBaselines(t, root); got["project"] != remote {
		t.Fatalf("origin baselines = %#v", got)
	}
}

func TestRunContextDoesNotGuessMissingOriginBaseline(t *testing.T) {
	root := newTestWorkspace(t)
	declared := testutil.InitFakeRemote(t, "declared", "main")
	local := testutil.InitFakeRemote(t, "local", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(declared, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	barePath := cloneTestProject(t, root, workspace.Projects["project"])
	if err := git.SetRemoteURL(barePath, local); err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(root, workspace)

	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Conflicts) != 1 || report.Conflicts[0].Operation != string(conflict.KindOriginDivergence) {
		t.Fatalf("conflicts = %+v", report.Conflicts)
	}
	if got, _ := git.ConfiguredRemoteURL(barePath, "origin"); got != local {
		t.Fatalf("local origin = %q, want unchanged %q", got, local)
	}
	if got := loadTestWorkspace(t, root).Projects["project"].Remote; got != declared {
		t.Fatalf("registry remote = %q, want unchanged %q", got, declared)
	}
}

func TestRefreshPlanIncludesProjectReceivedDuringWorkspaceSync(t *testing.T) {
	root := newTestWorkspace(t)
	workspace := &config.Workspace{Projects: map[string]config.Project{}}
	saveTestWorkspace(t, root, workspace)
	before := BuildPlan(root, workspace)
	remote := testutil.InitFakeRemote(t, "received", "main")
	workspace.Projects["received"] = activeProject(remote, "personal/received")
	saveTestWorkspace(t, root, workspace)

	plan := RefreshPlan(root, workspace, before)
	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if !git.IsRepo(filepath.Join(root, "personal", "received")) {
		t.Fatal("received project was not materialized")
	}
}

func TestRunContextClonesRelativeRemoteFromWorkspaceRoot(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "relative", "main")
	relative, err := filepath.Rel(root, remote)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(relative, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)

	report := newTestRunner(t, root).RunContext(context.Background(), NewSelection(plan, Probe(context.Background(), plan, nil)), nil)
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSuccess {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if !git.IsRepo(filepath.Join(root, "personal", "project")) {
		t.Fatal("relative remote project was not materialized")
	}
}

func cloneTestProject(t *testing.T, root string, project config.Project) string {
	t.Helper()
	barePath := layout.BarePath(filepath.Join(root, project.Path))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, project.Remote, barePath)
	return barePath
}

func saveTestOriginBaselines(t *testing.T, root string, baselines map[string]string) {
	t.Helper()
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveOriginBaselines(context.Background(), workspace.WorkspaceID, baselines); err != nil {
		t.Fatal(err)
	}
}

func loadTestOriginBaselines(t *testing.T, root string) map[string]string {
	t.Helper()
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	baselines, err := store.OriginBaselines(context.Background(), workspace.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return baselines
}
