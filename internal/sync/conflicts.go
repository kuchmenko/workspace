package sync

import (
	"encoding/json"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
)

func (r *Runner) recordProjectConflict(project, branch string, kind conflict.Kind, msg string) {
	if r.store == nil {
		return
	}
	msg = git.RedactDiagnostic(msg)
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
		r.logger.Printf("sync: record %s: %v", kind, err)
		return
	}
	if created {
		r.logger.Printf("sync: new conflict %s for %s/%s: %s", kind, project, branch, msg)
	}
}

func (r *Runner) clearProjectConflict(project, branch string, kind conflict.Kind) error {
	if r.store == nil {
		return nil
	}
	return r.store.Clear(r.root, project, branch, kind)
}
