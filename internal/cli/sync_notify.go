package cli

import (
	"fmt"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/conflict"
)

type syncConflictNotifier struct {
	store      *conflict.Store
	known      map[string]bool
	workspaces map[string]bool
}

func newSyncConflictNotifier(root string) *syncConflictNotifier {
	store, err := conflict.Open()
	if err != nil {
		return nil
	}
	conflicts, err := store.List()
	if err != nil {
		return nil
	}
	known := make(map[string]bool, len(conflicts))
	for _, stored := range conflicts {
		known[stored.ID] = true
	}
	return &syncConflictNotifier{store: store, known: known, workspaces: syncNotificationWorkspaces(root)}
}

func (n *syncConflictNotifier) notifyNew() {
	if n == nil {
		return
	}
	conflicts, err := n.store.List()
	if err != nil {
		return
	}
	for _, stored := range conflicts {
		if n.known[stored.ID] {
			continue
		}
		n.known[stored.ID] = true
		if n.workspaces[cleanAbsolutePath(stored.Workspace)] {
			notifySyncConflict(stored)
		}
	}
}

func syncNotificationWorkspaces(root string) map[string]bool {
	return map[string]bool{cleanAbsolutePath(root): true}
}

func cleanAbsolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func notifySyncConflict(stored conflict.Conflict) {
	title := fmt.Sprintf("ws: new sync conflict (%s)", stored.Kind)
	body := "workspace registry; run 'ws sync resolve'"
	if stored.Project != "" {
		body = fmt.Sprintf("%s/%s; run 'ws sync resolve'", stored.Project, stored.Branch)
	}
	notify(title, body)
}
