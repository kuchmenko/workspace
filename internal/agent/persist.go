package agent

import (
	"github.com/kuchmenko/workspace/internal/config"
)

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
