package sync

import (
	"context"
	"fmt"
	"maps"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

type appliedOrigin struct {
	repository string
	oldURL     string
}

func (r *Runner) applyWorkspaceConversion(ctx context.Context, selection *Selection, report *Report, onEvent func(Event)) map[string]string {
	converted := make(map[string]string)
	targetID := selection.plan.WorkspaceTargetID
	candidate, selected := selection.Conversion(targetID)
	if !selected {
		return converted
	}
	if !selection.verifiedConversion(targetID, candidate) {
		r.addConversion(report, *selection, targetID, candidate, ResultFailed, "SSH conversion was not verified", onEvent)
		selection.ExcludeTarget(targetID)
		return converted
	}
	report.start(Event{TargetID: targetID, Operation: "convert-origin"}, onEvent)
	if err := ctx.Err(); err != nil {
		r.addConversion(report, *selection, targetID, candidate, ResultCanceled, err.Error(), onEvent)
		selection.ExcludeTarget(targetID)
		return converted
	}
	target, _ := selection.target(targetID)
	old, err := applyRepositoryOrigin(target.Repository, target.ConfigURL, candidate)
	if err != nil {
		r.addConversion(report, *selection, targetID, candidate, ResultFailed, err.Error(), onEvent)
		selection.ExcludeTarget(targetID)
		return converted
	}
	if err := ctx.Err(); err != nil {
		_ = git.SetRemoteURL(target.Repository, old)
		r.addConversion(report, *selection, targetID, candidate, ResultCanceled, err.Error(), onEvent)
		selection.ExcludeTarget(targetID)
		return converted
	}
	converted[targetID] = candidate
	r.addConversion(report, *selection, targetID, candidate, ResultSuccess, "", onEvent)
	return converted
}

func (r *Runner) applyProjectConversions(ctx context.Context, selection *Selection, ws *config.Workspace, report *Report, onEvent func(Event)) map[string]string {
	converted := make(map[string]string)
	originals := make(map[string]config.Project)
	var applied []appliedOrigin
	for _, target := range selection.plan.Targets {
		if ctx.Err() != nil {
			return r.cancelProjectConversions(ctx, selection, ws, originals, applied, report, onEvent)
		}
		candidate, selected := selection.Conversion(target.ID)
		if !selected || target.Role != TargetProjectOrigin {
			continue
		}
		r.applyProjectConversion(ctx, selection, ws, target, candidate, originals, &applied, converted, report, onEvent)
	}
	if len(converted) == 0 {
		return converted
	}
	report.start(Event{Operation: "save-conversions"}, onEvent)
	if ctx.Err() != nil {
		return r.cancelProjectConversions(ctx, selection, ws, originals, applied, report, onEvent)
	}
	return r.saveProjectConversions(selection, ws, originals, applied, converted, report, onEvent)
}

func (r *Runner) saveProjectConversions(selection *Selection, ws *config.Workspace, originals map[string]config.Project, applied []appliedOrigin, converted map[string]string, report *Report, onEvent func(Event)) map[string]string {
	if err := config.Save(r.root, ws); err != nil {
		restoreOrigins(applied)
		for project, original := range originals {
			ws.Projects[project] = original
		}
		for _, target := range selection.plan.Targets {
			candidate, ok := converted[target.ID]
			if !ok {
				continue
			}
			r.addConversion(report, *selection, target.ID, candidate, ResultFailed, err.Error(), onEvent)
			selection.ExcludeProject(target.Project)
			delete(converted, target.ID)
		}
		r.addWorkspaceFailure(report, "save-conversions", err, onEvent)
		return converted
	}
	for _, target := range selection.plan.Targets {
		if candidate, ok := converted[target.ID]; ok {
			r.addConversion(report, *selection, target.ID, candidate, ResultSuccess, "", onEvent)
		}
	}
	return converted
}

func (r *Runner) applyProjectConversion(ctx context.Context, selection *Selection, ws *config.Workspace, target Target, candidate string, originals map[string]config.Project, applied *[]appliedOrigin, converted map[string]string, report *Report, onEvent func(Event)) {
	if !selection.verifiedConversion(target.ID, candidate) {
		r.addConversion(report, *selection, target.ID, candidate, ResultFailed, "SSH conversion was not verified", onEvent)
		selection.ExcludeProject(target.Project)
		return
	}
	report.start(Event{Project: target.Project, TargetID: target.ID, Operation: "convert-origin"}, onEvent)
	if err := ctx.Err(); err != nil {
		r.addConversion(report, *selection, target.ID, candidate, ResultCanceled, err.Error(), onEvent)
		selection.ExcludeProject(target.Project)
		return
	}
	planned, ok := projectPlan(selection.plan, target.Project)
	current, exists := ws.Projects[target.Project]
	if !ok || !exists || !snapshotMatches(planned.Snapshot, current) {
		r.addConversion(report, *selection, target.ID, candidate, ResultSkipped, "workspace.toml changed after preflight", onEvent)
		selection.ExcludeProject(target.Project)
		return
	}
	repository := conversionRepository(planned)
	if repository != "" {
		old, err := applyRepositoryOrigin(repository, target.ConfigURL, candidate)
		if err != nil {
			r.addConversion(report, *selection, target.ID, candidate, ResultFailed, err.Error(), onEvent)
			selection.ExcludeProject(target.Project)
			return
		}
		*applied = append(*applied, appliedOrigin{repository: repository, oldURL: old})
	}
	originals[target.Project] = current
	current.Remote = candidate
	ws.Projects[target.Project] = current
	converted[target.ID] = candidate
}

func (r *Runner) cancelProjectConversions(ctx context.Context, selection *Selection, ws *config.Workspace, originals map[string]config.Project, applied []appliedOrigin, report *Report, onEvent func(Event)) map[string]string {
	restoreOrigins(applied)
	for project, original := range originals {
		ws.Projects[project] = original
	}
	diagnostic := projectCancellation(ctx).Error()
	for _, target := range selection.plan.Targets {
		candidate, selected := selection.Conversion(target.ID)
		if !selected || target.Role != TargetProjectOrigin {
			continue
		}
		r.addConversion(report, *selection, target.ID, candidate, ResultCanceled, diagnostic, onEvent)
		selection.ExcludeProject(target.Project)
	}
	return map[string]string{}
}

func applyRepositoryOrigin(repository, expected, candidate string) (string, error) {
	old, err := git.ConfiguredRemoteURL(repository, "origin")
	if err != nil {
		return "", fmt.Errorf("read origin in %s: %w", repository, err)
	}
	if !git.RemoteBindingExact(repository, "origin", expected) {
		return "", fmt.Errorf("origin changed after preflight: got %q, want %q", git.RedactRemote(old), git.RedactRemote(expected))
	}
	if err := git.SetRemoteURL(repository, candidate); err != nil {
		return "", err
	}
	return old, nil
}

func restoreOrigins(origins []appliedOrigin) {
	for _, origin := range origins {
		_ = git.SetRemoteURL(origin.repository, origin.oldURL)
	}
}

func conversionRepository(project ProjectPlan) string {
	switch project.State {
	case ProjectPresent:
		return project.BarePath
	case ProjectNeedsMigration:
		return project.MainPath
	default:
		return ""
	}
}

func projectPlan(plan Plan, name string) (ProjectPlan, bool) {
	for _, project := range plan.Projects {
		if project.Name == name {
			return project, true
		}
	}
	return ProjectPlan{}, false
}

func snapshotMatches(expected ProjectSnapshot, project config.Project) bool {
	return expected.Remote == project.Remote &&
		expected.Path == project.Path &&
		expected.Status == project.Status &&
		expected.DefaultBranch == project.DefaultBranch &&
		maps.Equal(expected.Mirrors, project.Mirrors)
}

func (r *Runner) addConversion(report *Report, selection Selection, targetID, candidate string, status ResultStatus, diagnostic string, onEvent func(Event)) {
	target, _ := selection.target(targetID)
	result := ConversionResult{TargetID: targetID, Project: target.Project, From: git.RedactRemote(target.ConfigURL), To: git.RedactRemote(candidate), Status: status, Diagnostic: git.RedactDiagnostic(diagnostic, target.ConfigURL, candidate)}
	report.Conversions = append(report.Conversions, result)
	report.add(Event{Kind: EventConversion, Status: status, Project: target.Project, TargetID: targetID, Operation: "convert-origin", Diagnostic: result.Diagnostic}, onEvent)
}
