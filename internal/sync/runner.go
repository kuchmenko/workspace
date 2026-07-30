// Package sync synchronizes a workspace and its registered projects.
package sync

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

type Runner struct {
	root   string
	logger *log.Logger
	store  *conflict.Store
}

func NewRunner(root string, logger *log.Logger) *Runner {
	store, err := conflict.Open()
	if err != nil {
		logger.Printf("sync: cannot open conflicts store: %v", err)
	}
	return &Runner{root: root, logger: logger, store: store}
}

func (r *Runner) Run() Report {
	ws, err := config.Load(r.root)
	if err != nil {
		report := Report{}
		r.addWorkspaceFailure(&report, "load", err, nil)
		return report
	}
	plan := BuildPlan(r.root, ws)
	probes := Probe(context.Background(), plan, nil)
	selection := NewSelection(plan, probes)
	for _, target := range plan.Targets {
		selection.targets[target.ID] = target.Executable
	}
	for _, project := range plan.Projects {
		selection.projects[project.Name] = true
	}
	report := r.RunContext(context.Background(), selection, nil)
	for _, result := range append(append([]OperationResult{}, report.Workspace...), report.Projects...) {
		if result.Status == ResultFailed {
			r.logger.Printf("sync: %s failed: %s", result.Operation, result.Diagnostic)
		}
	}
	return report
}

func (r *Runner) RunContext(ctx context.Context, selection Selection, onEvent func(Event)) Report {
	report := Report{}
	if sc := sidecar.AnyActive(r.root); sc != nil {
		diagnostic := string(sc.Meta.Kind) + " in progress"
		result := OperationResult{Status: ResultSkipped, Operation: "sync", Reason: SkipSidecar, Diagnostic: diagnostic}
		report.Workspace = append(report.Workspace, result)
		report.add(Event{Kind: EventSkipped, Status: ResultSkipped, Operation: "sync", Reason: SkipSidecar, Diagnostic: diagnostic}, onEvent)
		return report
	}
	if err := ctx.Err(); err != nil {
		r.cancelReport(&report, err, onEvent)
		return report
	}
	ws, err := config.Load(r.root)
	if err != nil {
		r.addWorkspaceFailure(&report, "load", err, onEvent)
		return report
	}
	if err := frozenWorkspaceBranch(selection.plan); err != nil {
		r.addWorkspaceFailure(&report, "workspace-sync", err, onEvent)
		return report
	}
	converted := r.applyWorkspaceConversion(ctx, &selection, &report, onEvent)
	if ctx.Err() != nil {
		r.cancelReport(&report, ctx.Err(), onEvent)
		return report
	}
	for targetID, candidate := range r.applyProjectConversions(ctx, &selection, ws, &report, onEvent) {
		converted[targetID] = candidate
	}
	if ctx.Err() != nil {
		r.cancelReport(&report, ctx.Err(), onEvent)
		return report
	}
	r.syncSelectedWorkspace(ctx, selection, &report, onEvent)
	if ctx.Err() != nil {
		r.cancelReport(&report, ctx.Err(), onEvent)
		return report
	}
	ws, err = config.Load(r.root)
	if err != nil {
		r.addWorkspaceFailure(&report, "reload", err, onEvent)
		return report
	}
	r.recordValidationIssues(ws, &report, onEvent)
	r.runSelectedProjects(ctx, selection, converted, ws, &report, onEvent)
	return report
}

func frozenWorkspaceBranch(plan Plan) error {
	if plan.WorkspaceRepository == "" {
		return nil
	}
	branch, err := git.CurrentBranch(plan.WorkspaceRepository)
	if err != nil {
		return nil
	}
	if branch != plan.WorkspaceBranch {
		return fmt.Errorf("workspace branch changed after preflight: got %q, want %q", branch, plan.WorkspaceBranch)
	}
	return nil
}

func (r *Runner) syncSelectedWorkspace(ctx context.Context, selection Selection, report *Report, onEvent func(Event)) {
	if selection.plan.WorkspaceTargetID == "" {
		result := OperationResult{Status: ResultSkipped, Operation: "workspace-sync", Reason: SkipState, Diagnostic: "workspace origin was not planned"}
		report.Workspace = append(report.Workspace, result)
		report.add(Event{Kind: EventSkipped, Status: ResultSkipped, Operation: result.Operation, Reason: result.Reason, Diagnostic: result.Diagnostic}, onEvent)
		return
	}
	if !selection.TargetSelected(selection.plan.WorkspaceTargetID) {
		result := OperationResult{Status: ResultSkipped, Operation: "workspace-sync", TargetID: selection.plan.WorkspaceTargetID, Reason: SkipExcluded}
		report.Workspace = append(report.Workspace, result)
		report.add(Event{Kind: EventSkipped, Status: ResultSkipped, Operation: result.Operation, TargetID: result.TargetID, Reason: result.Reason}, onEvent)
		return
	}
	report.start(Event{Operation: "workspace-sync", TargetID: selection.plan.WorkspaceTargetID}, onEvent)
	target, _ := selection.target(selection.plan.WorkspaceTargetID)
	expectedURL := target.ConfigURL
	remoteURL := target.URL
	if candidate, converted := selection.Conversion(target.ID); converted {
		expectedURL = candidate
		remoteURL = candidate
	}
	_, err := r.syncTOMLContext(ctx, selection.plan.WorkspaceRepository, expectedURL, remoteURL, selection.plan.WorkspaceBranch)
	status := ResultSuccess
	reason := SkipReason("")
	diagnostic := ""
	if err != nil {
		status = ResultFailed
		if ctx.Err() != nil {
			status = ResultCanceled
			reason = SkipCanceled
		}
		diagnostic = err.Error()
	}
	result := OperationResult{Status: status, Operation: "workspace-sync", TargetID: selection.plan.WorkspaceTargetID, Reason: reason, Diagnostic: diagnostic}
	report.Workspace = append(report.Workspace, result)
	report.add(Event{Kind: EventWorkspace, Status: status, Operation: result.Operation, TargetID: result.TargetID, Reason: reason, Diagnostic: diagnostic}, onEvent)
}

func (r *Runner) runSelectedProjects(ctx context.Context, selection Selection, converted map[string]string, ws *config.Workspace, report *Report, onEvent func(Event)) {
	machine := loadMachineName()
	dirty := false
	plannedNames := make(map[string]bool, len(selection.plan.Projects))
	for _, planned := range selection.plan.Projects {
		plannedNames[planned.Name] = true
		touched, canceled := r.runPlannedProject(ctx, selection, converted, ws, planned, machine, report, onEvent)
		if canceled {
			return
		}
		if touched {
			dirty = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(ws.Projects)) {
		project := ws.Projects[name]
		if project.Status == config.StatusActive && !plannedNames[name] {
			r.addProjectSkip(report, name, SkipPlanChanged, "project was introduced after preflight", onEvent)
		}
	}
	if dirty {
		if err := config.Save(r.root, ws); err != nil {
			r.addWorkspaceFailure(report, "save-metadata", err, onEvent)
		}
	}
}

func (r *Runner) runPlannedProject(ctx context.Context, selection Selection, converted map[string]string, ws *config.Workspace, planned ProjectPlan, machine string, report *Report, onEvent func(Event)) (bool, bool) {
	if ctx.Err() != nil {
		r.cancelReport(report, ctx.Err(), onEvent)
		return false, true
	}
	if !selection.ProjectSelected(planned.Name) {
		r.addProjectSkip(report, planned.Name, SkipExcluded, "", onEvent)
		return false, false
	}
	report.start(Event{Project: planned.Name, Operation: "project-sync", TargetID: planned.OriginID}, onEvent)
	project, ok := ws.Projects[planned.Name]
	expected := planned.Snapshot
	if remote, changed := converted[planned.OriginID]; changed {
		expected.Remote = remote
		planned.OriginURL = remote
	}
	if !ok || !snapshotMatches(expected, project) {
		r.addProjectSkip(report, planned.Name, SkipPlanChanged, "workspace.toml changed after preflight", onEvent)
		return false, false
	}
	touched := false
	planned.Snapshot = expected
	result := r.syncPlannedProject(ctx, planned, &project, machine, &touched, selectedProjectMirrors(selection, planned), report, onEvent)
	report.Projects = append(report.Projects, result)
	report.add(Event{Kind: EventProject, Status: result.Status, Project: planned.Name, Operation: result.Operation, Reason: result.Reason, Diagnostic: result.Diagnostic}, onEvent)
	if result.Status == ResultCanceled || ctx.Err() != nil {
		r.cancelReport(report, projectCancellation(ctx), onEvent)
		return false, true
	}
	if touched {
		ws.Projects[planned.Name] = project
	}
	return touched, false
}

func projectCancellation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func selectedProjectMirrors(selection Selection, project ProjectPlan) map[string]bool {
	selected := make(map[string]bool, len(project.MirrorIDs))
	for _, id := range project.MirrorIDs {
		if target, ok := selection.target(id); ok {
			selected[target.Mirror] = selection.TargetSelected(id)
		}
	}
	return selected
}

func (r *Runner) recordValidationIssues(ws *config.Workspace, report *Report, onEvent func(Event)) {
	for _, issue := range ws.Validate() {
		if issue.Kind != config.ValidationDuplicateBranch {
			continue
		}
		r.recordProjectConflict(issue.Project, issue.Branch, conflict.KindBranchDuplicate, issue.Detail)
		result := OperationResult{Status: ResultFailed, Operation: string(conflict.KindBranchDuplicate), Project: issue.Project, Branch: issue.Branch, Diagnostic: issue.Detail}
		report.Conflicts = append(report.Conflicts, result)
		report.add(Event{Kind: EventConflict, Status: ResultFailed, Project: issue.Project, Branch: issue.Branch, Operation: result.Operation, Diagnostic: issue.Detail}, onEvent)
	}
}

func (r *Runner) addWorkspaceFailure(report *Report, operation string, err error, onEvent func(Event)) {
	result := OperationResult{Status: ResultFailed, Operation: operation, Diagnostic: err.Error()}
	report.Workspace = append(report.Workspace, result)
	report.add(Event{Kind: EventWorkspace, Status: ResultFailed, Operation: operation, Diagnostic: err.Error()}, onEvent)
}

func (r *Runner) addProjectSkip(report *Report, project string, reason SkipReason, diagnostic string, onEvent func(Event)) {
	result := OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: project, Reason: reason, Diagnostic: diagnostic}
	report.Projects = append(report.Projects, result)
	report.add(Event{Kind: EventSkipped, Status: ResultSkipped, Project: project, Operation: result.Operation, Reason: reason, Diagnostic: diagnostic}, onEvent)
}

func (r *Runner) cancelReport(report *Report, err error, onEvent func(Event)) {
	if report.Canceled {
		return
	}
	report.Canceled = true
	result := OperationResult{Status: ResultCanceled, Operation: "sync", Reason: SkipCanceled, Diagnostic: err.Error()}
	report.Workspace = append(report.Workspace, result)
	report.add(Event{Kind: EventCanceled, Status: ResultCanceled, Operation: "sync", Reason: SkipCanceled, Diagnostic: err.Error()}, onEvent)
}
