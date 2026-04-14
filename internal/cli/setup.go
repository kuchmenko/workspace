package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/setup"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup — select repos from GitHub and organize into groups",
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "First-time workspace setup: interactively select GitHub repos and organize them into groups",
			"agent:safety": "Interactive TUI — requires user interaction. Writes workspace.toml.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m := setup.NewModel()
			p := tea.NewProgram(m, tea.WithAltScreen())

			result, err := p.Run()
			if err != nil {
				return fmt.Errorf("TUI crashed: %w", err)
			}

			final := result.(setup.Model)
			r := final.GetResult()

			// Error from GitHub API or other failure
			if r.Err != nil {
				return fmt.Errorf("setup failed: %w", r.Err)
			}

			// User explicitly cancelled (ctrl+c, esc, n)
			if r.Cancelled {
				fmt.Println("Setup cancelled by user.")
				return nil
			}

			// Confirmed — write workspace.toml
			if !r.Confirmed {
				fmt.Println("Setup exited without confirmation.")
				return nil
			}

			for _, group := range r.Groups {
				ws.Groups[group.Name] = config.Group{
					Description: "",
				}

				for _, repo := range group.Repos {
					cat := config.CategoryWork
					if repo.Owner == r.Username {
						cat = config.CategoryPersonal
					}

					ws.Projects[repo.Name] = config.Project{
						Remote:   repo.SSHURL,
						Path:     group.Name + "/" + repo.Name,
						Status:   config.StatusActive,
						Category: cat,
						Group:    group.Name,
					}
				}
			}

			if err := saveWorkspace(); err != nil {
				return err
			}

			total := 0
			for _, g := range r.Groups {
				total += len(g.Repos)
			}

			fmt.Printf("\nWorkspace configured: %d groups, %d projects\n", len(r.Groups), total)
			fmt.Printf("Run 'ws sync' to clone all repos.\n")
			return nil
		},
	}
}
