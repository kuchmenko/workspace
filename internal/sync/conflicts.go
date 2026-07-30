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
		notifyConflict(c)
	}
}

func (r *Runner) clearProjectConflict(project, branch string, kind conflict.Kind) error {
	if r.store == nil {
		return nil
	}
	return r.store.Clear(r.root, project, branch, kind)
}

func (r *Runner) recordTOMLConflict(workspace string, kind conflict.Kind, cause error) {
	if r.store == nil {
		return
	}
	diagnostic := git.RedactDiagnostic(cause.Error())
	details, _ := json.Marshal(map[string]string{"error": diagnostic})
	c := conflict.Conflict{
		Workspace: workspace,
		Kind:      kind,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("sync: record conflict: %v", err)
		return
	}
	if created {
		r.logger.Printf("sync: new conflict %s in %s: %s", kind, workspace, diagnostic)
		notifyConflict(c)
	}
}

func (r *Runner) clearTOMLConflicts() error {
	if r.store == nil {
		return nil
	}
	for _, kind := range []conflict.Kind{conflict.KindTOMLMerge, conflict.KindTOMLPushFailed} {
		_ = r.store.Clear(r.root, "", "", kind)
	}
	return nil
}
