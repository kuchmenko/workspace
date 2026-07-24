package sync

import (
	"fmt"
	"os/exec"

	"codeberg.org/kuchmenko/workspace/internal/conflict"
)

func notifyConflict(c conflict.Conflict) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	title := fmt.Sprintf("ws: new sync conflict (%s)", c.Kind)
	body := "workspace.toml; run 'ws sync resolve'"
	if c.Project != "" {
		body = fmt.Sprintf("%s/%s; run 'ws sync resolve'", c.Project, c.Branch)
	}
	_ = exec.Command("notify-send", "-a", "ws", title, body).Run()
}
