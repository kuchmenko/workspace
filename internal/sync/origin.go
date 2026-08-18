package sync

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
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
	case shared == baseline && local == baseline:
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	case shared == baseline:
		project.Remote = local
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return true, nil
	case local == baseline:
		if err := git.SetRemoteURL(repository, shared); err != nil {
			result := failedProject(planned.Name, "update-origin", err)
			return false, &result
		}
		_ = r.clearProjectConflict(planned.Name, "", conflict.KindOriginDivergence)
		return false, nil
	case local == shared:
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
