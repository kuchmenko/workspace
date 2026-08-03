package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	wsRoot    string
	ws        *config.Workspace
	wsLoadErr error
)

var workspaceIndependentCommands = map[string]bool{
	"help": true, "completion": true, "docs": true,
	"explorer": true, "ws": true, "workspace": true,
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:               "ws",
		Short:             "Workspace manager — track, sync, and manage development projects",
		PersistentPreRunE: prepareCommand,

		RunE: func(cmd *cobra.Command, args []string) error {
			if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
				return runExplorerTUI()
			}
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
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
		newAliasCmd(),
		newMigrateCmd(),
		newWorktreeCmd(),
		newBootstrapCmd(),
		newExplorerCmd(),
		newFavoriteCmd(),
		newDocsCmd(),
		newDoctorCmd(),
		newWorkspaceCmd(),
	)

	return root
}

func prepareCommand(cmd *cobra.Command, _ []string) error {
	if commandSkipsWorkspace(cmd) {
		return nil
	}
	if cmd.Name() == "doctor" {
		return loadDoctorWorkspace()
	}
	if cmd.Name() == "setup" {
		return loadSetupWorkspace()
	}
	return loadCurrentWorkspace()
}

func commandSkipsWorkspace(cmd *cobra.Command) bool {
	if workspaceIndependentCommands[cmd.Name()] {
		return true
	}
	if cmd.Parent() == nil {
		return false
	}
	parent := cmd.Parent().Name()
	return parent == "explorer" || parent == "workspace" || parent == "auth"
}

func loadDoctorWorkspace() error {
	if err := findWorkspaceRoot(); err != nil {
		return err
	}
	ws, wsLoadErr = config.Load(wsRoot)
	return nil
}

func loadSetupWorkspace() error {
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

func loadCurrentWorkspace() error {
	if err := findWorkspaceRoot(); err != nil {
		return err
	}
	var err error
	ws, err = config.Load(wsRoot)
	if err != nil {
		return err
	}
	wsLoadErr = nil
	return nil
}

func findWorkspaceRoot() error {
	if wsRoot != "" {
		return nil
	}
	var err error
	wsRoot, err = config.FindRoot()
	return err
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		var exitErr ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, "ws:", err)
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

	return nil
}
