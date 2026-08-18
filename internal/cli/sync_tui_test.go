package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	workspacesync "github.com/kuchmenko/workspace/internal/sync"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestSyncModelTransitionsFromProbeToReview(t *testing.T) {
	plan, probes := syncModelFixture()
	model := newSyncModel(context.Background(), t.TempDir(), plan)
	defer model.cancelProbe()

	updated, _ := model.Update(syncProbeEventMsg{event: workspacesync.ProbeEvent{Kind: workspacesync.ProbeStarted, EndpointID: "project-endpoint", URL: "file:///repo"}})
	model = updated.(syncModel)
	if model.probeStarted != 1 || model.currentProbe != "file:///repo" {
		t.Fatalf("probe progress = started %d current %q", model.probeStarted, model.currentProbe)
	}
	updated, _ = model.Update(syncProbeDoneMsg{report: probes})
	model = updated.(syncModel)
	if model.stage != syncReview {
		t.Fatalf("stage = %v, want review", model.stage)
	}
	if len(model.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(model.rows))
	}
	if !model.selection.ProjectSelected("app") {
		t.Fatal("accessible project was not selected")
	}
}

func TestSyncModelTogglesProjectAndSource(t *testing.T) {
	plan, probes := syncModelFixture()
	model := reviewSyncModel(t, plan, probes)
	model.cursor = rowIndex(model.rows, syncProjectRow)

	updated, _ := model.Update(tui.KeyMsg{Type: tui.KeySpace})
	model = updated.(syncModel)
	if model.selection.ProjectSelected("app") {
		t.Fatal("project remained selected after toggle")
	}
	updated, _ = model.Update(tui.KeyMsg{Type: tui.KeySpace})
	model = updated.(syncModel)
	if !model.selection.ProjectSelected("app") {
		t.Fatal("project was not selected after second toggle")
	}

	model.cursor = rowIndex(model.rows, syncSourceRow)
	updated, _ = model.Update(tui.KeyMsg{Type: tui.KeySpace})
	model = updated.(syncModel)
	if model.selection.ProjectSelected("app") || model.selection.TargetSelected("project:app:mirror:backup") {
		t.Fatal("source exclusion left a target selected")
	}
}

func TestSyncModelSelectsAndRemovesVerifiedConversion(t *testing.T) {
	plan, probes := syncModelFixture()
	probes.Results[0] = workspacesync.ProbeResult{
		EndpointID:      "project-endpoint",
		URL:             "https://codeberg.org/example/app.git",
		Status:          workspacesync.ProbeAccess,
		Candidate:       "git@codeberg.org:example/app.git",
		CandidateStatus: workspacesync.ProbeSuccess,
	}
	model := reviewSyncModel(t, plan, probes)
	model.cursor = rowIndex(model.rows, syncProjectRow)

	updated, _ := model.Update(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'c'}})
	model = updated.(syncModel)
	if candidate, ok := model.selection.Conversion("project:app:origin"); !ok || candidate != "git@codeberg.org:example/app.git" {
		t.Fatalf("conversion = %q, %v", candidate, ok)
	}
	if !model.selection.ProjectSelected("app") {
		t.Fatal("verified conversion did not enable project")
	}

	updated, _ = model.Update(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'c'}})
	model = updated.(syncModel)
	if _, ok := model.selection.Conversion("project:app:origin"); ok {
		t.Fatal("conversion remained selected")
	}
	if model.selection.ProjectSelected("app") {
		t.Fatal("project remained selected after removing failed-origin conversion")
	}
}

func TestSyncModelCancelWaitsForRunnerAndFinishes(t *testing.T) {
	plan, probes := syncModelFixture()
	model := reviewSyncModel(t, plan, probes)
	model.stage = syncRunning
	model.runMessages = make(chan tui.Msg, 1)
	canceled := false
	model.cancelRun = func() { canceled = true }

	updated, cmd := model.Update(tui.KeyMsg{Type: tui.KeyCtrlC, Ctrl: true})
	model = updated.(syncModel)
	if !canceled || model.stage != syncCanceling || cmd != nil {
		t.Fatalf("cancel state = canceled %v stage %v cmd %v", canceled, model.stage, cmd != nil)
	}
	report := workspacesync.Report{Canceled: true}
	updated, _ = model.Update(syncRunDoneMsg{report: report})
	model = updated.(syncModel)
	if model.stage != syncFinished || !model.canceled {
		t.Fatalf("final cancel state = stage %v canceled %v", model.stage, model.canceled)
	}
}

func TestSyncModelCtrlCOnFinishedSummaryPreservesSuccess(t *testing.T) {
	model := syncModel{
		stage:  syncFinished,
		report: workspacesync.Report{Projects: []workspacesync.OperationResult{{Status: workspacesync.ResultSuccess}}},
	}

	updated, cmd := model.Update(tui.KeyMsg{Type: tui.KeyCtrlC, Ctrl: true})
	model = updated.(syncModel)
	if cmd == nil {
		t.Fatal("ctrl+c on finished summary did not quit")
	}
	if model.canceled {
		t.Fatal("ctrl+c on finished summary changed the result to canceled")
	}
	_, code := renderSyncTUIResult(model)
	if code != 0 {
		t.Fatalf("result code = %d, want success", code)
	}
}

func TestRenderSyncTUIResultLeavesPlainPreExecutionCancellation(t *testing.T) {
	text, code := renderSyncTUIResult(syncModel{stage: syncReview, canceled: true})
	if code != syncExitCanceled {
		t.Fatalf("result code = %d, want %d", code, syncExitCanceled)
	}
	if text != "Sync canceled before execution\n" {
		t.Fatalf("result text = %q", text)
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatal("cancellation result contains ANSI")
	}
}

func TestRenderRecentEventEscapesProjectAndMirrorControls(t *testing.T) {
	got := renderRecentEvent(workspacesync.Event{
		Kind:      workspacesync.EventMirror,
		Status:    workspacesync.ResultSuccess,
		Operation: "mirror-push",
		Project:   "app\n\x1b]8;;https://example.com\x07",
		Mirror:    "backup\u009dunsafe",
	})

	if strings.ContainsAny(got, "\n\u009d") {
		t.Fatalf("recent event contains project or mirror controls: %q", got)
	}
	if !strings.Contains(got, `app\x0A\x1B]8;;https://example.com\x07/backup\x9Dunsafe`) {
		t.Fatalf("recent event did not escape project and mirror controls: %q", got)
	}
}

func TestSyncDashboardProgressCountsSelectedProjectsOnce(t *testing.T) {
	plan, probes := syncModelFixture()
	model := reviewSyncModel(t, plan, probes)
	updated, _ := model.beginRun()
	model = updated.(syncModel)
	defer model.cancelRun()

	if model.total != 1 || model.completed != 0 {
		t.Fatalf("initial progress = %d/%d, want 0/1", model.completed, model.total)
	}
	for _, event := range []workspacesync.Event{
		{Kind: workspacesync.EventWorkspace, Status: workspacesync.ResultSuccess, Operation: "workspace-sync"},
		{Kind: workspacesync.EventConversion, Status: workspacesync.ResultSuccess, Project: "app", Operation: "convert-origin"},
		{Kind: workspacesync.EventMirror, Status: workspacesync.ResultFailed, Project: "app", Mirror: "backup", Operation: "mirror-push"},
		{Kind: workspacesync.EventSkipped, Status: workspacesync.ResultSkipped, Project: "introduced-after-preflight", Operation: "project-sync"},
	} {
		model.updateRunEvent(event)
	}
	if model.completed != 0 {
		t.Fatalf("non-project outcomes changed progress to %d/%d", model.completed, model.total)
	}
	model.updateRunEvent(workspacesync.Event{Kind: workspacesync.EventProject, Status: workspacesync.ResultSuccess, Project: "app", Operation: "project-sync"})
	model.updateRunEvent(workspacesync.Event{Kind: workspacesync.EventProject, Status: workspacesync.ResultSuccess, Project: "app", Operation: "project-sync"})
	if model.completed != 1 || model.total != 1 {
		t.Fatalf("final progress = %d/%d, want 1/1", model.completed, model.total)
	}
	if model.counters.success != 4 || model.counters.failed != 1 || model.counters.skipped != 1 {
		t.Fatalf("counters = %+v", model.counters)
	}
	if view := model.viewDashboard(false); !bytes.Contains([]byte(view), []byte("Progress: 1/1")) {
		t.Fatalf("dashboard progress missing from view:\n%s", view)
	}
}

func TestSyncDashboardProgressCountsSelectedPlanChangeSkip(t *testing.T) {
	plan, probes := syncModelFixture()
	model := reviewSyncModel(t, plan, probes)
	updated, _ := model.beginRun()
	model = updated.(syncModel)
	defer model.cancelRun()

	model.updateRunEvent(workspacesync.Event{Kind: workspacesync.EventSkipped, Status: workspacesync.ResultSkipped, Project: "app", Operation: "project-sync", Reason: workspacesync.SkipPlanChanged})
	if model.completed != 1 || model.total != 1 {
		t.Fatalf("skipped selected project progress = %d/%d, want 1/1", model.completed, model.total)
	}
}

func TestSyncModelFinalSummary(t *testing.T) {
	model := syncModel{
		stage:   syncFinished,
		elapsed: 2,
		report: workspacesync.Report{
			Projects:  []workspacesync.OperationResult{{Status: workspacesync.ResultSuccess}},
			Mirrors:   []workspacesync.OperationResult{{Status: workspacesync.ResultFailed}},
			Conflicts: []workspacesync.OperationResult{{Status: workspacesync.ResultFailed}},
		},
	}
	view := model.View()
	for _, text := range []string{"Completed with failures", "Success: 1", "Failed: 1", "Conflicts: 1"} {
		if !strings.Contains(view, text) {
			t.Errorf("summary missing %q:\n%s", text, view)
		}
	}
}

func reviewSyncModel(t *testing.T, plan workspacesync.Plan, probes workspacesync.ProbeReport) syncModel {
	t.Helper()
	model := newSyncModel(context.Background(), t.TempDir(), plan)
	model.cancelProbe()
	model.probes = probes
	model.selection = workspacesync.NewSelection(plan, probes)
	model.rows = buildSyncRows(plan)
	model.stage = syncReview
	return model
}

func rowIndex(rows []syncReviewRow, kind syncRowKind) int {
	for index, row := range rows {
		if row.kind == kind {
			return index
		}
	}
	return -1
}

func syncModelFixture() (workspacesync.Plan, workspacesync.ProbeReport) {
	plan := workspacesync.Plan{
		Projects: []workspacesync.ProjectPlan{{
			Name:      "app",
			State:     workspacesync.ProjectMissing,
			OriginID:  "project:app:origin",
			MirrorIDs: []string{"project:app:mirror:backup"},
		}},
		Targets: []workspacesync.Target{
			{ID: "project:app:origin", Role: workspacesync.TargetProjectOrigin, Project: "app", URL: "file:///repo", EndpointID: "project-endpoint", SourceKey: "local", Executable: true},
			{ID: "project:app:mirror:backup", Role: workspacesync.TargetMirror, Project: "app", Mirror: "backup", URL: "file:///mirror", EndpointID: "mirror-endpoint", SourceKey: "local", Executable: true},
		},
		Endpoints: []workspacesync.Endpoint{
			{ID: "project-endpoint", URL: "file:///repo", SourceKey: "local", Executable: true, TargetIDs: []string{"project:app:origin"}},
			{ID: "mirror-endpoint", URL: "file:///mirror", SourceKey: "local", Executable: true, TargetIDs: []string{"project:app:mirror:backup"}},
		},
		SourceGroups: []workspacesync.SourceGroup{{Key: "local", EndpointIDs: []string{"project-endpoint", "mirror-endpoint"}, TargetIDs: []string{"project:app:origin", "project:app:mirror:backup"}}},
	}
	probes := workspacesync.ProbeReport{Results: []workspacesync.ProbeResult{
		{EndpointID: "project-endpoint", URL: "file:///repo", Status: workspacesync.ProbeSuccess},
		{EndpointID: "mirror-endpoint", URL: "file:///mirror", Status: workspacesync.ProbeSuccess},
	}}
	return plan, probes
}
