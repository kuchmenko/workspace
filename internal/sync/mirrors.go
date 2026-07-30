package sync

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
)

func (r *Runner) syncMirrorsContext(ctx context.Context, name string, project *config.Project, barePath string, frozenURLs map[string]string, selected map[string]bool, report *Report, onEvent func(Event)) {
	r.clearStaleMirrorConflicts(name, project.Mirrors)
	for _, mirror := range slices.Sorted(maps.Keys(project.Mirrors)) {
		url := project.Mirrors[mirror]
		if !selected[mirror] {
			r.addMirrorResult(report, onEvent, name, mirror, ResultSkipped, SkipExcluded, "")
			continue
		}
		if err := ctx.Err(); err != nil {
			r.addMirrorResult(report, onEvent, name, mirror, ResultCanceled, SkipCanceled, err.Error())
			return
		}
		if report != nil {
			report.start(Event{Project: name, Mirror: mirror, Operation: "mirror-push"}, onEvent)
		}
		if err := git.EnsureMirrorRemote(barePath, mirror, url); err != nil {
			r.recordMirrorConflict(name, mirror, url, err)
			r.addMirrorFailure(report, onEvent, name, mirror, err)
			continue
		}
		if err := git.PushMirrorURLContext(ctx, barePath, frozenURLs[mirror]); err != nil {
			if ctx.Err() != nil {
				r.addMirrorResult(report, onEvent, name, mirror, ResultCanceled, SkipCanceled, ctx.Err().Error())
				return
			}
			r.recordMirrorConflict(name, mirror, url, err)
			r.addMirrorFailure(report, onEvent, name, mirror, err)
			continue
		}
		_ = r.clearProjectConflict(name, mirror, conflict.KindMirrorPushFailed)
		r.addMirrorResult(report, onEvent, name, mirror, ResultSuccess, "", "")
	}
}

func (r *Runner) addMirrorFailure(report *Report, onEvent func(Event), project, mirror string, err error) {
	diagnostic := git.RedactDiagnostic(err.Error())
	r.addMirrorResult(report, onEvent, project, mirror, ResultFailed, "", diagnostic)
	r.addConflictResult(report, project, mirror, conflict.KindMirrorPushFailed, diagnostic, onEvent)
}

func (r *Runner) addMirrorResult(report *Report, onEvent func(Event), project, mirror string, status ResultStatus, reason SkipReason, diagnostic string) {
	if report == nil {
		return
	}
	result := OperationResult{Status: status, Operation: "mirror-push", Project: project, Mirror: mirror, Reason: reason, Diagnostic: diagnostic}
	report.Mirrors = append(report.Mirrors, result)
	report.add(Event{Kind: EventMirror, Status: status, Project: project, Mirror: mirror, Operation: result.Operation, Reason: reason, Diagnostic: diagnostic}, onEvent)
}

func (r *Runner) clearStaleMirrorConflicts(name string, mirrors map[string]string) {
	if r.store == nil {
		return
	}
	conflicts, err := r.store.List()
	if err != nil {
		return
	}
	for _, stored := range conflicts {
		if stored.Kind != conflict.KindMirrorPushFailed || stored.Workspace != r.root || stored.Project != name {
			continue
		}
		if _, still := mirrors[stored.Branch]; !still {
			_ = r.store.Remove(stored.ID)
		}
	}
}

func (r *Runner) recordMirrorConflict(project, mirror, url string, cause error) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"message": git.RedactDiagnostic(cause.Error(), url), "mirror": mirror, "url": git.RedactRemote(url)})
	stored := conflict.Conflict{Workspace: r.root, Project: project, Branch: mirror, Kind: conflict.KindMirrorPushFailed, Details: details}
	created, err := r.store.Record(stored)
	if err != nil {
		r.logger.Printf("sync: record %s: %v", conflict.KindMirrorPushFailed, err)
		return
	}
	if created {
		notifyConflict(stored)
	}
}
