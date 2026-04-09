package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "TUI launcher for Claude Code sessions across workspaces",
		Long: `Launch an interactive TUI that lets you browse workspaces, projects,
and worktrees, then start or resume Claude Code sessions.

Navigation: j/k to move, Enter to open, h/Esc to go back, q to quit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTUI()
		},
	}
}

func runAgentTUI() error {
	cwd, _ := os.Getwd()
	workspaces, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	m := agent.NewModel(workspaces)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
