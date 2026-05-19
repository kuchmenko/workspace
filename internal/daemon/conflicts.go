package daemon

import (
	"encoding/json"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
)

func (r *Reconciler) recordValidationIssues(ws *config.Workspace) {
	for _, issue := range ws.Validate() {
		switch issue.Kind {
		case config.ValidationDuplicateBranch:
			r.recordProjectConflict(issue.Project, issue.Branch, conflict.KindBranchDuplicate, issue.Detail)
		}
	}
}

func (r *Reconciler) recordProjectConflict(project, branch string, kind conflict.Kind, msg string) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"message": msg})
	c := conflict.Conflict{
		Workspace: r.root,
		Project:   project,
		Branch:    branch,
		Kind:      kind,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("reconciler: record %s: %v", kind, err)
		return
	}
	if created {
		r.logger.Printf("reconciler: NEW conflict %s for %s/%s: %s", kind, project, branch, msg)
		conflict.NotifyNew(c)
	}
}

func (r *Reconciler) clearProjectConflict(project, branch string, kind conflict.Kind) error {
	if r.store == nil {
		return nil
	}
	return r.store.Clear(r.root, project, branch, kind)
}

func (r *Reconciler) recordBackoff(name string, cause error) {
	bs, ok := r.backoff[name]
	if !ok {
		bs = &backoffState{currentDelay: r.interval}
		r.backoff[name] = bs
	} else {
		bs.currentDelay *= 2
		if bs.currentDelay > r.maxInterval {
			bs.currentDelay = r.maxInterval
		}
	}
	bs.nextAllowedAt = time.Now().Add(bs.currentDelay)
	r.logger.Printf("reconciler: %s failed (%v); next attempt in %s", name, cause, bs.currentDelay)
}

func (r *Reconciler) resetBackoff(name string) {
	delete(r.backoff, name)
}
