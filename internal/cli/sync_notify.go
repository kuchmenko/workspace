package cli

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/conflict"
)

type syncConflictNotifier struct {
	root  string
	store *conflict.Store
	known map[string]bool
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
	return &syncConflictNotifier{root: root, store: store, known: known}
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
		if stored.Workspace == n.root {
			notifySyncConflict(stored)
		}
	}
}

func notifySyncConflict(stored conflict.Conflict) {
	title := fmt.Sprintf("ws: new sync conflict (%s)", stored.Kind)
	body := "workspace.toml; run 'ws sync resolve'"
	if stored.Project != "" {
		body = fmt.Sprintf("%s/%s; run 'ws sync resolve'", stored.Project, stored.Branch)
	}
	notify(title, body)
}
