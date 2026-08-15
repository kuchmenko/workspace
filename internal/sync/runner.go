// Package sync synchronizes a workspace and its registered projects.
package sync

import (
	"context"
	"log"
	"maps"
	"slices"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

type Runner struct {
	root      string
	logger    *log.Logger
	store     *conflict.Store
	registry  *registry.Store
	workspace registry.Workspace
}

func NewRunner(root string, logger *log.Logger) *Runner {
	store, err := conflict.Open()
	if err != nil {
		logger.Printf("sync: cannot open conflicts store: %v", err)
	}
	return &Runner{root: root, logger: logger, store: store}
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
	local, err := registry.OpenDefault()
	if err != nil {
		r.addWorkspaceFailure(&report, "load", err, onEvent)
		return report
	}
	defer func() { _ = local.Close() }()
	r.workspace, err = local.LoadByRoot(ctx, r.root)
	if err != nil {
		r.addWorkspaceFailure(&report, "load", err, onEvent)
		return report
	}
	r.registry = local
	ws := r.workspace.State
	converted := r.applyProjectConversions(ctx, &selection, ws, &report, onEvent)
	if ctx.Err() != nil {
		r.cancelReport(&report, ctx.Err(), onEvent)
		return report
	}
	r.recordValidationIssues(ws, &report, onEvent)
	r.runSelectedProjects(ctx, selection, converted, ws, &report, onEvent)
	return report
}

func (r *Runner) saveRegistry(workspace *config.Workspace) error {
	updated, err := r.registry.Update(context.Background(), r.workspace.Name, r.workspace.Revision, workspace)
	if err == nil {
		r.workspace = updated
	}
	return err
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
		if err := r.saveRegistry(ws); err != nil {
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
		r.addProjectSkip(report, planned.Name, SkipPlanChanged, "workspace registry changed after preflight", onEvent)
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
