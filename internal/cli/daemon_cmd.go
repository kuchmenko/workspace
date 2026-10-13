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
	command.AddCommand(newDaemonRunCmd(), newDaemonInstallCmd(), newDaemonUninstallCmd())
	return command
}

func newDaemonInstallCmd() *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "install",
		Short: "Install and start background sync for your user account",
		Long: "Install and start the daemon with systemd on Linux or launchd on macOS.\n" +
			"The daemon syncs every registered workspace and clones missing active projects.\n" +
			"Dirty, diverged, and unsupported checkouts are left unchanged.\n" +
			"Run this from the installed ws binary, not a temporary build.\n" +
			"Running install again updates the service and restarts the daemon.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selected, err := networkDeviceName(name)
			if err != nil {
				return err
			}
			if err = daemon.InstallService(command.Context(), selected); err != nil {
				return err
			}
			fmt.Fprintln(command.OutOrStdout(), "Installed and started the daemon. Background sync is enabled at login.")
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	return command
}

func newDaemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop background sync and remove its user service",
		Long:  "Stop the daemon and remove its systemd unit or LaunchAgent.\nWorkspaces, repositories, credentials, logs, and the ws binary are kept.",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := daemon.UninstallService(command.Context()); err != nil {
				return err
			}
			fmt.Fprintln(command.OutOrStdout(), "Removed the daemon service. Workspaces and repositories are unchanged.")
			return nil
		},
	}
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
