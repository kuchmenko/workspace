package cli

import (
	"encoding/json"
	"os"

	"github.com/kuchmenko/workspace/internal/docs"
	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	var agent bool

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation from the command tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent {
				return runDocsAgent(cmd)
			}
			return cmd.Help()
		},
	}

	cmd.Flags().BoolVar(&agent, "agent", false, "emit JSON capability map for AI agents")
	return cmd
}

func runDocsAgent(cmd *cobra.Command) error {
	m := docs.GenerateAgentCapabilityMap(cmd.Root())

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}
