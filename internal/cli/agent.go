package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "TUI launcher for Claude Code sessions across workspaces",
		Annotations: map[string]string{
			"capability":   "agent",
			"agent:when":   "Browse workspaces and projects, then launch or resume Claude Code sessions",
			"agent:safety": "Interactive TUI. Use subcommands (launch, shell, resume) for non-interactive access.",
		},
		Long: `Launch an interactive TUI that lets you browse workspaces, projects,
and worktrees, then start or resume Claude Code sessions.

Navigation: j/k to move, Enter to open, h/Esc to go back, q to quit.
Subcommands provide non-interactive access to the same actions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTUI()
		},
	}
	cmd.AddCommand(
		newAgentLaunchCmd(),
		newAgentShellCmd(),
		newAgentResumeCmd(),
	)
	return cmd
}

func newAgentLaunchCmd() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "launch <project-path>",
		Short: "Launch claude in a project directory (non-interactive)",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Start a new Claude Code session in a specific project directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.LaunchClaude(args[0], "", prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "initial prompt for claude")
	return cmd
}

func newAgentShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <path>",
		Short: "Open shell in a directory (non-interactive)",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Open a new shell in a specific project directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.LaunchShell(args[0])
		},
	}
}

func newAgentResumeCmd() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a Claude Code session by ID",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Resume a previously started Claude Code session by its session ID",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			session := agent.FindSession(sessionID)
			if session == nil {
				return fmt.Errorf("session %s not found", sessionID)
			}
			return agent.LaunchClaude(session.Cwd, session.ID, prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "additional prompt for the resumed session")
	return cmd
}

func runAgentTUI() error {
	cwd, _ := os.Getwd()
	workspaces, sessCache, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	m := agent.NewModel(workspaces, sessCache)
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	// If the user selected a launch action, exec into claude now.
	// bubbletea has already restored the terminal at this point.
	if final, ok := finalModel.(*agent.Model); ok && final.Launch != nil {
		if final.Launch.ShellOnly {
			return agent.LaunchShell(final.Launch.Cwd)
		}
		return agent.LaunchClaude(final.Launch.Cwd, final.Launch.ResumeID, final.Launch.Prompt)
	}
	return nil
}
