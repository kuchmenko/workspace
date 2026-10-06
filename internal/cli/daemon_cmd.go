package cli

import (
	"fmt"
	"time"

	"github.com/kuchmenko/workspace/internal/daemon"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "daemon",
		Short: "Run continuous peer services",
	}
	command.AddCommand(newDaemonRunCmd())
	return command
}

func newDaemonRunCmd() *cobra.Command {
	var name, listen string
	var interval, gitInterval, discoveryWindow time.Duration
	command := &cobra.Command{
		Use:   "run",
		Short: "Serve peers and synchronize workspace registries",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err = networkDeviceName(name)
			if err != nil {
				return err
			}
			ctx, stop := networkSignalContext(command.Context())
			defer stop()
			return daemon.Run(ctx, daemon.Options{
				Store: store, Identity: identity, Name: name, ListenAddress: listen,
				SyncInterval: interval, GitSyncInterval: gitInterval, DiscoveryWindow: discoveryWindow,
				Ready: func(endpoint string) { fmt.Fprintf(command.OutOrStdout(), "Daemon available at %s.\n", endpoint) },
				Logf:  func(format string, args ...any) { fmt.Fprintf(command.ErrOrStderr(), format+"\n", args...) },
			})
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	command.Flags().StringVar(&listen, "listen", peernetwork.DefaultListenAddress, "peer listen address")
	command.Flags().DurationVar(&interval, "sync-interval", daemon.DefaultSyncInterval, "registry repair interval")
	command.Flags().DurationVar(&gitInterval, "git-sync-interval", daemon.DefaultGitSyncInterval, "published Git state repair interval")
	command.Flags().DurationVar(&discoveryWindow, "discovery-window", daemon.DefaultDiscoveryWindow, "peer discovery window")
	return command
}
