package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/mattn/go-isatty"
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
			if cmd.Name() == "help" || cmd.Name() == "completion" || cmd.Name() == "docs" {
				return nil
			}

			if cmd.Name() == "agent" || cmd.Name() == "ws" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "daemon" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
				return nil
			}

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

		RunE: func(cmd *cobra.Command, args []string) error {
			if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
				return runExplorerTUI()
			}
			return cmd.Help()
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&wsRoot, "root", "", "workspace root directory (default: auto-detect)")

	root.AddCommand(
		newSyncCmd(),
		newAddCmd(),
		newCreateCmd(),
		newPathCmd(),
		newStatusCmd(),
		newScanCmd(),
		newSetupCmd(),
		newAuthCmd(),
		newDaemonCmd(),
		newAliasCmd(),
		newMigrateCmd(),
		newWorktreeCmd(),
		newBootstrapCmd(),
		newExplorerCmd(),
		newFavoriteCmd(),
		newDocsCmd(),
		newDoctorCmd(),
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

	if err := alias.WriteStateFile(ws, wsRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update alias state file: %v\n", err)
	}

	if client, err := daemon.Dial(); err == nil {
		_ = client.Notify(wsRoot, "config_changed")
		client.Close()
	}
	return nil
}
