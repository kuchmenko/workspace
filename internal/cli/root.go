package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/syncnode"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	wsRoot    string
	ws        *config.Workspace
	wsLoadErr error
	nodeStore *syncnode.Store
	nodeState syncnode.Workspace
	nodeID    syncnode.Identity
)

var workspaceIndependentCommands = map[string]bool{
	"help": true, "completion": true, "docs": true,
	"explorer": true, "ws": true, "workspace": true,
}

const skipsWorkspaceAnnotation = "ws.skips-workspace"

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
	if cmd.Annotations[skipsWorkspaceAnnotation] == "true" {
		return true
	}
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
	if nodeStore != nil {
		_ = nodeStore.Close()
		nodeStore = nil
		nodeState = syncnode.Workspace{}
		nodeID = syncnode.Identity{}
	}
	loaded, err := loadCurrentNodeWorkspace()
	if err != nil {
		return err
	}
	if loaded {
		wsLoadErr = nil
		return nil
	}
	if err = findWorkspaceRoot(); err != nil {
		return err
	}
	ws, err = config.Load(wsRoot)
	if err != nil {
		return err
	}
	wsLoadErr = nil
	return nil
}

func loadCurrentNodeWorkspace() (bool, error) {
	paths, err := syncnode.DefaultPaths()
	if err != nil {
		return false, err
	}
	if _, err = os.Stat(paths.Database); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	store, err := syncnode.OpenStore(paths.Database)
	if err != nil {
		return false, err
	}
	var loaded syncnode.Workspace
	if wsRoot != "" {
		loaded, err = store.LoadByRoot(context.Background(), wsRoot)
	} else {
		var cwd string
		cwd, err = os.Getwd()
		if err == nil {
			loaded, err = findNodeWorkspace(context.Background(), store, cwd)
		}
	}
	if errors.Is(err, syncnode.ErrWorkspaceNotFound) {
		store.Close()
		return false, nil
	}
	if err != nil {
		store.Close()
		return false, err
	}
	identity, err := syncnode.OpenOrCreateIdentity(paths.Identity)
	if err != nil {
		store.Close()
		return false, err
	}
	nodeStore = store
	nodeState = loaded
	nodeID = identity
	wsRoot = loaded.Root
	ws = loaded.State
	return true, nil
}

func findNodeWorkspace(ctx context.Context, store *syncnode.Store, path string) (syncnode.Workspace, error) {
	workspaces, err := store.List(ctx)
	if err != nil {
		return syncnode.Workspace{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return syncnode.Workspace{}, err
	}
	var found syncnode.Workspace
	for _, candidate := range workspaces {
		relative, relErr := filepath.Rel(candidate.Root, abs)
		if relErr == nil && relative != ".." && !filepath.IsAbs(relative) && (found.Root == "" || len(candidate.Root) > len(found.Root)) {
			found = candidate
		}
	}
	if found.Root == "" {
		return syncnode.Workspace{}, syncnode.ErrWorkspaceNotFound
	}
	return found, nil
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
	root := NewRootCmd()
	started := time.Now()
	cmd, err := root.ExecuteC()
	path := "ws"
	if cmd != nil {
		path = cmd.CommandPath()
	}
	metrics.RecordCommand(path, commandTerminal(), commandOutcome(err), time.Since(started))
	if err != nil {
		var exitErr ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, "ws:", err)
		os.Exit(1)
	}
}

func commandTerminal() bool {
	stdinTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	stdoutTTY := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	return stdinTTY && stdoutTTY
}

func commandOutcome(err error) metrics.Outcome {
	if err == nil {
		return metrics.Success
	}
	var exitErr ExitError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &exitErr) && exitErr.Code == 130 {
		return metrics.Canceled
	}
	return metrics.Failure
}

func saveWorkspace() error {
	if nodeStore != nil && nodeState.Root == wsRoot {
		committed, err := nodeStore.Commit(context.Background(), nodeState.Name, nodeState.Head, ws, nodeID)
		if err != nil {
			return fmt.Errorf("saving workspace revision: %w", err)
		}
		nodeState = committed
		ws = committed.State
	} else if err := config.Save(wsRoot, ws); err != nil {
		return fmt.Errorf("saving workspace.toml: %w", err)
	}

	if err := alias.WriteStateFile(ws, wsRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update alias state file: %v\n", err)
	} else {
		metrics.RecordAliasStateGenerated()
	}

	return nil
}
