package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "sync",
		Short:       "Synchronize workspace state with peers",
		Annotations: agentAnnotations("sync", AgentInteractionNone, AgentApprovalNone, AgentEffectNone, AgentEffectNone, "text", "1"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("peer sync is not implemented yet for workspace %q", nodeState.Name)
		},
	}
}
