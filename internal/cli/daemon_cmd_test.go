package cli

import (
	"testing"

	"github.com/kuchmenko/workspace/internal/daemon"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
)

func TestDaemonRunDefaults(t *testing.T) {
	command := newDaemonRunCmd()
	for name, want := range map[string]string{
		"listen":            peernetwork.DefaultListenAddress,
		"sync-interval":     daemon.DefaultSyncInterval.String(),
		"git-sync-interval": daemon.DefaultGitSyncInterval.String(),
		"discovery-window":  daemon.DefaultDiscoveryWindow.String(),
	} {
		flag := command.Flag(name)
		if flag == nil || flag.DefValue != want {
			t.Fatalf("%s default = %v, want %q", name, flag, want)
		}
	}
}
