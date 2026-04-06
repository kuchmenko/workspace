package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the workspace daemon",
	}

	cmd.AddCommand(
		newDaemonRunCmd(),
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonRestartCmd(),
		newDaemonStatusCmd(),
		newDaemonRegisterCmd(),
		newDaemonUnregisterCmd(),
		newDaemonInstallServiceCmd(),
	)

	return cmd
}

func newDaemonRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run daemon in foreground (used by systemd)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.Run()
		},
	}
}

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if pid, running := daemon.IsRunning(); running {
				return fmt.Errorf("daemon already running (pid %d)", pid)
			}
			_, err := daemon.StartBackground()
			return err
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.Dial()
			if err != nil {
				// Try to check PID file
				if pid, running := daemon.IsRunning(); running {
					proc, _ := os.FindProcess(pid)
					proc.Signal(os.Interrupt)
					fmt.Printf("  Sent interrupt to pid %d\n", pid)
					return nil
				}
				return fmt.Errorf("daemon not running")
			}
			defer client.Close()
			if err := client.Stop(); err != nil {
				return err
			}
			fmt.Println("  Daemon stopped.")
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Stop if running
			if client, err := daemon.Dial(); err == nil {
				client.Stop()
				client.Close()
			}
			// Start
			_, err := daemon.StartBackground()
			return err
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.Dial()
			if err != nil {
				if pid, running := daemon.IsRunning(); running {
					fmt.Printf("  Daemon running (pid %d) but socket unreachable\n", pid)
				} else {
					fmt.Println("  Daemon not running.")
				}
				return nil
			}
			defer client.Close()

			status, err := client.Status()
			if err != nil {
				return err
			}

			fmt.Printf("  Running (pid %d)\n", status.PID)
			fmt.Printf("  Workspaces: %d\n", len(status.Workspaces))
			for _, w := range status.Workspaces {
				fmt.Printf("    %s (auto_sync=%v)\n", w.Root, w.AutoSync)
			}
			return nil
		},
	}
}

func newDaemonRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "register [path]",
		Short: "Register a workspace with the daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := wsRoot
			if len(args) == 1 {
				root = args[0]
			}
			if err := daemon.RegisterWorkspace(root); err != nil {
				return err
			}
			fmt.Printf("  Registered: %s\n", root)
			return nil
		},
	}
}

func newDaemonUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister [path]",
		Short: "Unregister a workspace from the daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := wsRoot
			if len(args) == 1 {
				root = args[0]
			}
			if err := daemon.UnregisterWorkspace(root); err != nil {
				return err
			}
			fmt.Printf("  Unregistered: %s\n", root)
			return nil
		},
	}
}

func newDaemonInstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-service",
		Short: "Install systemd user service for auto-start",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			unitDir := filepath.Join(home, ".config", "systemd", "user")
			if err := os.MkdirAll(unitDir, 0o755); err != nil {
				return err
			}

			unitContent := `[Unit]
Description=ws workspace manager daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/ws daemon run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
			unitPath := filepath.Join(unitDir, "ws-daemon.service")
			if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
				return err
			}
			fmt.Printf("  Installed: %s\n", unitPath)

			// Enable and start
			exec.Command("systemctl", "--user", "daemon-reload").Run()
			if err := exec.Command("systemctl", "--user", "enable", "--now", "ws-daemon").Run(); err != nil {
				fmt.Println("  Unit installed. Enable manually: systemctl --user enable --now ws-daemon")
				return nil
			}
			fmt.Println("  Service enabled and started.")
			return nil
		},
	}
}
