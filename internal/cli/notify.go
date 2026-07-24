package cli

import "os/exec"

func notify(title, body string) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	_ = exec.Command("notify-send", "-a", "ws", title, body).Run()
}
