package cli

import (
	"fmt"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var (
		filterStatus   string
		filterCategory string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects with optional filters",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Projects) == 0 {
				fmt.Println("No projects registered.")
				return nil
			}

			names := make([]string, 0, len(ws.Projects))
			for name := range ws.Projects {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				proj := ws.Projects[name]

				if filterStatus != "" && string(proj.Status) != filterStatus {
					continue
				}
				if filterCategory != "" && string(proj.Category) != filterCategory {
					continue
				}

				fmt.Printf("  %s  %s  %s  %s\n", name, proj.Status, proj.Category, proj.Remote)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&filterStatus, "status", "", "filter by status: active, archived, dormant")
	cmd.Flags().StringVar(&filterCategory, "category", "", "filter by category: personal, work")

	// Register valid values for shell completion
	cmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{
			string(config.StatusActive),
			string(config.StatusArchived),
			string(config.StatusDormant),
		}, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("category", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{
			string(config.CategoryPersonal),
			string(config.CategoryWork),
		}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
