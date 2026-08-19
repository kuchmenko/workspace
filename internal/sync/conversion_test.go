package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/testutil"
)

const testHTTPSRemote = "https://github.com/acme/workspace-sync-test.git"

func TestRunContextAppliesVerifiedProjectConversionConsistently(t *testing.T) {
	root, workspace, barePath, candidate := setupConvertibleProject(t, false)
	plan := BuildPlan(root, workspace)
	probes := Probe(context.Background(), plan, nil)
	selection := NewSelection(plan, probes)
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatalf("SelectConversion: %v; probes=%+v", err, probes)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSuccess {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	loaded := loadTestWorkspace(t, root)
	if got := loaded.Projects["project"].Remote; got != candidate {
		t.Fatalf("workspace remote = %q, want %q", got, candidate)
	}
	if got, err := git.RemoteURL(barePath); err != nil || got != candidate {
		t.Fatalf("bare origin = %q, %v; want %q", got, err, candidate)
	}
}

func TestRunContextConversionUpdatesBlockedRegistryOnly(t *testing.T) {
	root, workspace, _, candidate := setupConvertibleProject(t, true)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}
	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSuccess {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	loaded := loadTestWorkspace(t, root)
	if loaded.Projects["project"].Remote != candidate {
		t.Fatalf("registry conversion not saved: %+v", loaded.Projects["project"])
	}
	if data, err := os.ReadFile(filepath.Join(root, "blocked")); err != nil || string(data) != "occupied" {
		t.Fatalf("blocked path changed: %q, %v", data, err)
	}
}

func TestRunContextAppliesVerifiedPlainCheckoutConversion(t *testing.T) {
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "plain-ssh-target", "main")
	configureTestSSH(t, remote)
	plainPath := testutil.InitFakePlainCheckout(t, filepath.Join(root, "personal"), "project", []string{"main"})
	testutil.RunGit(t, plainPath, "remote", "add", "origin", testHTTPSRemote)
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(testHTTPSRemote, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSuccess {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	candidate := "git@github.com:acme/workspace-sync-test.git"
	if got, err := git.ConfiguredRemoteURL(plainPath, "origin"); err != nil || got != candidate {
		t.Fatalf("plain origin = %q, %v; want %q", got, err, candidate)
	}
}

func TestRunContextDoesNotConvertPlanChangedProject(t *testing.T) {
	root, workspace, barePath, _ := setupConvertibleProject(t, false)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}
	changed := workspace.Projects["project"]
	changed.DefaultBranch = "trunk"
	workspace.Projects["project"] = changed
	saveTestWorkspace(t, root, workspace)

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSkipped {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	if got, err := git.ConfiguredRemoteURL(barePath, "origin"); err != nil || got != testHTTPSRemote {
		t.Fatalf("plan-changed bare origin = %q, %v", got, err)
	}
}

func TestRunContextConvertsIncomingOriginFromLocalBaseline(t *testing.T) {
	root := newTestWorkspace(t)
	baseline := testutil.InitFakeRemote(t, "baseline", "main")
	candidateRemote := testutil.InitFakeRemote(t, "incoming", "main")
	configureTestSSH(t, candidateRemote)
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(baseline, "personal/project")}}
	saveTestWorkspace(t, root, workspace)
	barePath := layout.BarePath(filepath.Join(root, "personal/project"))
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.CloneBare(t, baseline, barePath)
	baselines := map[string]string{"project": baseline}
	saveTestOriginBaselines(t, root, baselines)
	before := BuildPlanWithBaselines(root, workspace, baselines)
	project := workspace.Projects["project"]
	project.Remote = testHTTPSRemote
	workspace.Projects["project"] = project
	saveTestWorkspace(t, root, workspace)
	plan := RefreshPlan(root, workspace, before)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatalf("SelectConversion: %v", err)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSuccess {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	want := "git@github.com:acme/workspace-sync-test.git"
	if got, err := git.ConfiguredRemoteURL(barePath, "origin"); err != nil || got != want {
		t.Fatalf("origin = %q, %v; want %q", got, err, want)
	}
	if got := loadTestWorkspace(t, root).Projects["project"].Remote; got != want {
		t.Fatalf("registry remote = %q, want %q", got, want)
	}
}

func TestRunContextFailedProjectConversionSkipsTarget(t *testing.T) {
	root, workspace, barePath, _ := setupConvertibleProject(t, false)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(barePath); err != nil {
		t.Fatal(err)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultFailed {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	if len(report.Projects) != 1 || report.Projects[0].Status != ResultSkipped || report.Projects[0].Reason != SkipExcluded {
		t.Fatalf("projects = %+v", report.Projects)
	}
	if _, err := os.Stat(barePath); !os.IsNotExist(err) {
		t.Fatalf("failed original URL target executed: %v", err)
	}
}

func TestRunContextCanceledConversionsRestoreOriginsBeforeSave(t *testing.T) {
	root := newTestWorkspace(t)
	sshRemote := testutil.InitFakeRemote(t, "conversion-cancel", "main")
	configureTestSSH(t, sshRemote)
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"alpha": activeProject(testHTTPSRemote, "alpha"),
		"omega": activeProject(testHTTPSRemote, "omega"),
	}}
	for _, project := range workspace.Projects {
		barePath := layout.BarePath(filepath.Join(root, project.Path))
		testutil.CloneBare(t, sshRemote, barePath)
		if err := git.SetRemoteURL(barePath, testHTTPSRemote); err != nil {
			t.Fatal(err)
		}
	}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	for _, project := range plan.Projects {
		if err := selection.SelectConversion(project.OriginID); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	report := newTestRunner(t, root).RunContext(ctx, selection, func(event Event) {
		if event.Kind == EventStarted && event.Operation == "save-conversions" {
			cancel()
		}
	})
	if !report.Canceled || len(report.Conversions) != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, project := range plan.Projects {
		if got, err := git.ConfiguredRemoteURL(project.BarePath, "origin"); err != nil || got != testHTTPSRemote {
			t.Fatalf("%s origin = %q, %v", project.Name, got, err)
		}
	}
	loaded := loadTestWorkspace(t, root)
	for name, project := range loaded.Projects {
		if project.Remote != testHTTPSRemote {
			t.Fatalf("%s registry remote = %q", name, project.Remote)
		}
	}
}

func TestRunContextCancellationStartsNoLaterConversion(t *testing.T) {
	root := newTestWorkspace(t)
	sshRemote := testutil.InitFakeRemote(t, "conversion-stop", "main")
	configureTestSSH(t, sshRemote)
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"alpha": activeProject(testHTTPSRemote, "alpha"),
		"omega": activeProject(testHTTPSRemote, "omega"),
	}}
	saveTestWorkspace(t, root, workspace)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	for _, project := range plan.Projects {
		if err := selection.SelectConversion(project.OriginID); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var started []string
	report := newTestRunner(t, root).RunContext(ctx, selection, func(event Event) {
		if event.Kind == EventStarted && event.Operation == "convert-origin" {
			started = append(started, event.Project)
			cancel()
		}
	})
	if !report.Canceled {
		t.Fatalf("report = %+v", report)
	}
	if !equalStrings(started, []string{"alpha"}) {
		t.Fatalf("started conversions = %v", started)
	}
}

func TestRunContextRestoresOriginWhenConversionSaveFails(t *testing.T) {
	root, workspace, barePath, _ := setupConvertibleProject(t, false)
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}

	report := newTestRunner(t, root).RunContext(context.Background(), selection, func(event Event) {
		if event.Kind == EventStarted && event.Operation == "save-conversions" {
			saveTestWorkspace(t, root, workspace)
		}
	})
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultFailed {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	if got, err := git.ConfiguredRemoteURL(barePath, "origin"); err != nil || got != testHTTPSRemote {
		t.Fatalf("bare origin after save failure = %q, %v", got, err)
	}
}

func setupConvertibleProject(t *testing.T, blocked bool) (string, *config.Workspace, string, string) {
	t.Helper()
	root := newTestWorkspace(t)
	remote := testutil.InitFakeRemote(t, "ssh-target", "main")
	configureTestSSH(t, remote)
	path := "personal/project"
	if blocked {
		path = "blocked"
		if err := os.WriteFile(filepath.Join(root, path), []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workspace := &config.Workspace{Projects: map[string]config.Project{"project": activeProject(testHTTPSRemote, path)}}
	saveTestWorkspace(t, root, workspace)
	barePath := layout.BarePath(filepath.Join(root, path))
	if !blocked {
		if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
			t.Fatal(err)
		}
		testutil.CloneBare(t, remote, barePath)
		if err := git.SetRemoteURL(barePath, testHTTPSRemote); err != nil {
			t.Fatal(err)
		}
	}
	return root, workspace, barePath, "git@github.com:acme/workspace-sync-test.git"
}

func configureTestSSH(t *testing.T, remote string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec git-upload-pack \"$WS_TEST_REMOTE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WS_TEST_REMOTE", remote)
	t.Setenv("GIT_SSH_COMMAND", script)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file:///definitely-missing/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/")
}
