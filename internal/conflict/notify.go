package conflict

import (
	"fmt"
	"os/exec"
)

func Notify(title, body string) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	_ = exec.Command("notify-send", "-a", "ws", title, body).Run()
}

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
