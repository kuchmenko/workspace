package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspace roots registered on this machine",
	}
	cmd.AddCommand(
		newWorkspaceAddCmd(),
		newWorkspaceRemoveCmd(),
		newWorkspaceListCmd(),
		newWorkspaceImportCmd(),
		newWorkspaceExportCmd(),
	)
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "add [path]",
		Short:       "Register a workspace root on this machine",
		Annotations: agentAnnotations("workspace-register", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "path", "0,1"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := workspacePathArg(args)
			if err != nil {
				return err
			}
			root, err := config.AddWorkspaceRoot(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), root)
			return nil
		},
	}
}

func newWorkspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "rm [path]",
		Aliases:     []string{"remove"},
		Short:       "Remove a workspace root from this machine",
		Annotations: agentAnnotations("workspace-unregister", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "path", "0,1"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := workspacePathArg(args)
			if err != nil {
				return err
			}
			root, err := config.RemoveWorkspaceRoot(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), root)
			return nil
		},
	}
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List workspace roots registered on this machine",
		Annotations: agentAnnotations("workspace-list", AgentInteractionNone, AgentApprovalNone, AgentEffectNone, AgentEffectNone, "lines", "0,1"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			roots, err := config.ListWorkspaceRoots()
			if err != nil {
				return err
			}
			for _, root := range roots {
				fmt.Fprintln(cmd.OutOrStdout(), root)
			}
			return nil
		},
	}
}

func workspacePathArg(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	return os.Getwd()
}

func newWorkspaceImportCmd() *cobra.Command {
	var name, root, recoveryKey string
	cmd := &cobra.Command{
		Use:   "import <workspace.toml>",
		Short: "Import TOML into the local revision store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if root == "" {
				var err error
				root, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			body, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			workspace, err := config.DecodeWorkspaceForImport(body)
			if err != nil {
				return err
			}
			recoveryPublicKey, created, err := openOrCreateRecoveryKey(recoveryKey)
			if err != nil {
				return err
			}
			paths, err := syncnode.DefaultPaths()
			if err != nil {
				return err
			}
			identity, err := syncnode.OpenOrCreateIdentity(paths.Identity)
			if err != nil {
				return err
			}
			store, err := syncnode.OpenStore(paths.Database)
			if err != nil {
				return err
			}
			defer store.Close()
			imported, err := store.Import(cmd.Context(), name, root, workspace, identity, recoveryPublicKey)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "workspace=%s id=%s head=%s root=%s\n", imported.Name, imported.ID, imported.Head, imported.Root)
			if created {
				fmt.Fprintf(cmd.OutOrStdout(), "recovery key created at %s; move it off this node before enabling peer sync\n", recoveryKey)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "local workspace name")
	cmd.Flags().StringVar(&root, "root", "", "local workspace root (default: current directory)")
	cmd.Flags().StringVar(&recoveryKey, "recovery-key", "", "offline recovery key path (created when missing)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("recovery-key")
	return cmd
}

func newWorkspaceExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <name>",
		Short: "Export a local workspace as TOML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := syncnode.DefaultPaths()
			if err != nil {
				return err
			}
			store, err := syncnode.OpenStore(paths.Database)
			if err != nil {
				return err
			}
			defer store.Close()
			workspace, err := store.LoadByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body, err := config.EncodeCanonicalWorkspace(workspace.State)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
}

func openOrCreateRecoveryKey(path string) ([]byte, bool, error) {
	if path == "" {
		return nil, false, errors.New("recovery key path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	publicKey, err := syncnode.LoadRecoveryKey(absolute)
	if err == nil {
		return publicKey, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	publicKey, err = syncnode.CreateRecoveryKey(absolute)
	return publicKey, err == nil, err
}
