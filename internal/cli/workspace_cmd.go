package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/config"
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
