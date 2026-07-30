package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestProbeLocalUnsupportedAndCancellation(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "reachable", "main")
	plan := BuildPlan(t.TempDir(), &config.Workspace{Projects: map[string]config.Project{
		"reachable": activeProject(remote, "reachable"),
		"missing":   activeProject(filepath.Join(t.TempDir(), "missing.git"), "missing"),
		"invalid":   activeProject("ftp://example.com/repo.git", "invalid"),
	}})
	report := ProbeWithWorkers(context.Background(), plan, 2, nil)
	statuses := probeStatusesByProject(plan, report)
	if statuses["reachable"] != ProbeSuccess {
		t.Errorf("reachable = %s", statuses["reachable"])
	}
	if statuses["missing"] != ProbeUnreachable {
		t.Errorf("missing = %s", statuses["missing"])
	}
	if statuses["invalid"] != ProbeUnsupported {
		t.Errorf("invalid = %s", statuses["invalid"])
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := ProbeWithWorkers(ctx, plan, 2, nil)
	for _, result := range canceled.Results {
		if result.Status != ProbeCanceled && result.Status != ProbeUnsupported {
			t.Errorf("canceled result %s = %s", result.EndpointID, result.Status)
		}
	}
}

func TestProbeRespectsWorkerBoundAndReturnsPlanOrder(t *testing.T) {
	projects := make(map[string]config.Project)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		projects[name] = activeProject(testutil.InitFakeRemote(t, name, "main"), name)
	}
	plan := BuildPlan(t.TempDir(), &config.Workspace{Projects: projects})
	active := 0
	maximum := 0
	report := ProbeWithWorkers(context.Background(), plan, 2, func(event ProbeEvent) {
		switch event.Kind {
		case ProbeStarted:
			active++
			maximum = max(maximum, active)
		case ProbeFinished:
			active--
		}
	})
	if maximum > 2 {
		t.Fatalf("maximum concurrent probes = %d, want <= 2", maximum)
	}
	for index, result := range report.Results {
		if result.EndpointID != plan.Endpoints[index].ID {
			t.Fatalf("result %d = %s, want %s", index, result.EndpointID, plan.Endpoints[index].ID)
		}
	}
}

func TestSelectionDefaultsAndExclusions(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "reachable", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"enabled":  activeProject(remote, "enabled"),
		"disabled": activeProject(filepath.Join(t.TempDir(), "missing.git"), "disabled"),
	}}
	plan := BuildPlan(t.TempDir(), workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if !selection.ProjectSelected("enabled") || selection.ProjectSelected("disabled") {
		t.Fatalf("unexpected defaults: enabled=%v disabled=%v", selection.ProjectSelected("enabled"), selection.ProjectSelected("disabled"))
	}
	selection.ExcludeProject("enabled")
	if selection.ProjectSelected("enabled") {
		t.Fatal("excluded project remains selected")
	}

	selection = NewSelection(plan, Probe(context.Background(), plan, nil))
	selection.ExcludeSource("local")
	if selection.ProjectSelected("enabled") {
		t.Fatal("source exclusion did not disable project")
	}
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err == nil {
		t.Fatal("unverified conversion was accepted")
	}
}

func TestSelectionOriginSourceExclusionClearsChildMirrors(t *testing.T) {
	origin := testutil.InitFakeRemote(t, "selection-origin", "main")
	sshRemote := testutil.InitFakeRemote(t, "selection-mirror", "main")
	configureTestSSH(t, sshRemote)
	project := activeProject(origin, "project")
	project.Mirrors = map[string]string{"backup": "git@github.com:acme/workspace-sync-test.git"}
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": project}}
	plan := BuildPlan(t.TempDir(), workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	mirrorID := plan.Projects[0].MirrorIDs[0]
	if !selection.ProjectSelected("project") || !selection.TargetSelected(mirrorID) {
		t.Fatalf("unexpected initial selection: project=%v mirror=%v", selection.ProjectSelected("project"), selection.TargetSelected(mirrorID))
	}
	selection.ExcludeSource("local")
	if selection.ProjectSelected("project") || selection.TargetSelected(mirrorID) {
		t.Fatalf("origin exclusion left child selected: project=%v mirror=%v", selection.ProjectSelected("project"), selection.TargetSelected(mirrorID))
	}
	if err := selection.IncludeTarget(mirrorID); err == nil {
		t.Fatal("mirror was included under an excluded project")
	}
}

func TestSelectionChoicesAreSortedCopies(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "reachable", "main")
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"zeta":  activeProject(remote, "zeta"),
		"alpha": activeProject(remote, "alpha"),
	}}
	plan := BuildPlan(t.TempDir(), workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	projects := selection.SelectedProjects()
	if !equalStrings(projects, []string{"alpha", "zeta"}) {
		t.Fatalf("selected projects = %v", projects)
	}
	projects[0] = "changed"
	if got := selection.SelectedProjects()[0]; got != "alpha" {
		t.Fatalf("selection was mutated through returned choices: %s", got)
	}
}

func probeStatusesByProject(plan Plan, report ProbeReport) map[string]ProbeStatus {
	statuses := make(map[string]ProbeStatus)
	for _, project := range plan.Projects {
		for _, target := range plan.Targets {
			if target.ID != project.OriginID {
				continue
			}
			result, _ := report.result(target.EndpointID)
			statuses[project.Name] = result.Status
		}
	}
	return statuses
}
