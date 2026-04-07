package conflict

import (
	"fmt"
	"os/exec"
)

// Notify sends a desktop notification via notify-send if it is installed.
// Failure (no notify-send, no display, etc.) is silent — the conflict is
// already persisted to conflicts.json, so the notification is purely a
// nice-to-have.
func Notify(title, body string) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	_ = exec.Command("notify-send", "-a", "ws", title, body).Run()
}

// NotifyNew is a convenience helper for the reconciler: a single-line
// summary of a freshly recorded conflict.
func NotifyNew(c Conflict) {
	title := fmt.Sprintf("ws: new sync conflict (%s)", c.Kind)
	var body string
	if c.Project != "" {
		body = fmt.Sprintf("%s/%s — run 'ws sync resolve'", c.Project, c.Branch)
	} else {
		body = "workspace.toml — run 'ws sync resolve'"
	}
	Notify(title, body)
}
