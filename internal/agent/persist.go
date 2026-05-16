package agent

import (
	"github.com/kuchmenko/workspace/internal/config"
)

// MutateAndSave runs `apply` against a freshly loaded Workspace, then
// persists the result if `apply` reports a change. The daemon is
// notified best-effort so the next reconciler tick observes the new
// state immediately. Used by `ws agent` to flip favorites and view
// preference without leaking the load/save dance into the TUI layer.
//
// `apply` returns true to signal "in-memory state moved, please save".
// Returning false skips the write entirely — a clean no-op.
//
// Errors from Load and Save propagate. The daemon notify is best-effort
// and never surfaces (a missing daemon is a recoverable, expected state
// during fresh installs and tests).
func MutateAndSave(wsRoot string, apply func(*config.Workspace) bool) error {
	ws, err := config.Load(wsRoot)
	if err != nil {
		return err
	}
	if !apply(ws) {
		return nil
	}
	if err := config.Save(wsRoot, ws); err != nil {
		return err
	}
	notifyDaemon(wsRoot)
	return nil
}
