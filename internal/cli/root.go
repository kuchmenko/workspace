package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/spf13/cobra"
)

var (
	wsRoot string
	ws     *config.Workspace
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ws",
		Short: "Workspace manager — track, sync, and manage development projects",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip loading for commands that don't need it
			// Commands that don't need workspace.toml
			if cmd.Name() == "help" || cmd.Name() == "completion" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "daemon" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
				return nil
			}

			// Setup bootstraps its own workspace — use cwd, create if needed
			if cmd.Name() == "setup" {
				var err error
				if wsRoot == "" {
					wsRoot, err = os.Getwd()
					if err != nil {
						return err
					}
				}
				ws, err = config.LoadOrCreate(wsRoot)
				return err
			}

			var err error
			if wsRoot == "" {
				wsRoot, err = config.FindRoot()
				if err != nil {
					return err
				}
			}
			ws, err = config.Load(wsRoot)
			if err != nil {
				return err
			}
			return nil
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&wsRoot, "root", "", "workspace root directory (default: auto-detect)")

	root.AddCommand(
		newSyncCmd(),
		newAddCmd(),
		newArchiveCmd(),
		newRestoreCmd(),
		newStatusCmd(),
		newScanCmd(),
		newCleanCmd(),
		newListCmd(),
		newGroupCmd(),
		newSetupCmd(),
		newAuthCmd(),
		newDaemonCmd(),
		newAliasCmd(),
		newMigrateCmd(),
		newWorktreeCmd(),
		newBootstrapCmd(),
		newPulseCmd(),
	)

	return root
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func saveWorkspace() error {
	if err := config.Save(wsRoot, ws); err != nil {
		return fmt.Errorf("saving workspace.toml: %w", err)
	}
	// Regenerate alias state file so shells stay in sync. Best-effort.
	if err := alias.WriteStateFile(ws, wsRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update alias state file: %v\n", err)
	}
	// Best-effort daemon notification
	if client, err := daemon.Dial(); err == nil {
		client.Notify(wsRoot, "config_changed")
		client.Close()
	}
	return nil
}
