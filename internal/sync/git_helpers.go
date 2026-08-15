package sync

import (
	"github.com/kuchmenko/workspace/internal/config"
)

func loadMachineName() string {
	mc, err := config.LoadMachineConfig()
	if err != nil || mc == nil {
		return ""
	}
	return mc.MachineName
}
