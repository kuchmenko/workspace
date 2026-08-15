package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage local SQLite workspaces",
	}
	cmd.AddCommand(
		newWorkspaceCreateCmd(),
		newWorkspaceListCmd(),
		newWorkspaceImportCmd(),
		newWorkspaceExportCmd(),
	)
	return cmd
}

func newWorkspaceCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:         "create [path]",
		Aliases:     []string{"add"},
		Short:       "Create a local SQLite workspace",
		Annotations: agentAnnotations("workspace-create", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "text", "0,1"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := workspacePathArg(args)
			if err != nil {
				return err
			}
			root, err = filepath.Abs(root)
			if err != nil {
				return err
			}
			if err = os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			local, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = local.Close() }()
			workspace := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
			createdWorkspace, err := local.Create(cmd.Context(), name, root, workspace)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "workspace=%s root=%s\n", createdWorkspace.Name, createdWorkspace.Root)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "local workspace name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List local SQLite workspaces",
		Annotations: agentAnnotations("workspace-list", AgentInteractionNone, AgentApprovalNone, AgentEffectNone, AgentEffectNone, "lines", "0,1"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			local, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = local.Close() }()
			workspaces, err := local.List(cmd.Context())
			if err != nil {
				return err
			}
			for _, workspace := range workspaces {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", workspace.Name, workspace.Root)
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
	var name, root string
	cmd := &cobra.Command{
		Use:   "import <workspace.toml>",
		Short: "Import TOML into the local SQLite registry",
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
			store, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			imported, err := store.Create(cmd.Context(), name, root, workspace)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "workspace=%s root=%s\n", imported.Name, imported.Root)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "local workspace name")
	cmd.Flags().StringVar(&root, "root", "", "local workspace root (default: current directory)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newWorkspaceExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <name>",
		Short: "Export a local workspace as TOML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			workspace, err := store.LoadByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body, err := config.EncodeWorkspace(workspace.State)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
}
