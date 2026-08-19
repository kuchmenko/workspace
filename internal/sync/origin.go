package sync

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/registry"
)

func (r *Runner) reconcileProjectOrigin(planned ProjectPlan, project *config.Project, report *Report, onEvent func(Event)) (bool, *OperationResult) {
	repository := conversionRepository(planned)
	if repository == "" || planned.LocalOrigin == "" {
		return false, nil
	}
	if err := exactOrigin(repository, planned.LocalOrigin); err != nil {
		result := OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: err.Error()}
		return false, &result
	}
	baseline := planned.BaselineRemote
	local := planned.LocalOrigin
	shared := project.Remote
	switch {
	case baseline == "" && local == shared:
		r.origins[planned.Name] = shared
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	case baseline == "":
		diagnostic := fmt.Sprintf("origin baseline is missing while local is %q and shared is %q", git.RedactRemote(local), git.RedactRemote(shared))
		r.recordProjectConflict(planned.Name, "", conflict.KindOriginDivergence, diagnostic)
		r.addConflictResult(report, planned.Name, "", conflict.KindOriginDivergence, diagnostic, onEvent)
		result := OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: diagnostic}
		return false, &result
	case shared == baseline && local == baseline:
		r.origins[planned.Name] = shared
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	case shared == baseline:
		if registry.RemoteContainsCredentials(local) {
			diagnostic := "local origin contains credentials and cannot be shared"
			r.recordProjectConflict(planned.Name, "", conflict.KindOriginDivergence, diagnostic)
			r.addConflictResult(report, planned.Name, "", conflict.KindOriginDivergence, diagnostic, onEvent)
			result := OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: diagnostic}
			return false, &result
		}
		project.Remote = local
		r.origins[planned.Name] = local
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return true, nil
	case local == baseline:
		if err := git.SetRemoteURL(repository, shared); err != nil {
			result := failedProject(planned.Name, "update-origin", err)
			return false, &result
		}
		r.origins[planned.Name] = shared
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	case local == shared:
		r.origins[planned.Name] = shared
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	default:
		diagnostic := fmt.Sprintf("origin changed locally to %q and remotely to %q from %q", git.RedactRemote(local), git.RedactRemote(shared), git.RedactRemote(baseline))
		r.recordProjectConflict(planned.Name, "", conflict.KindOriginDivergence, diagnostic)
		r.addConflictResult(report, planned.Name, "", conflict.KindOriginDivergence, diagnostic, onEvent)
		result := OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: diagnostic}
		return false, &result
	}
}
