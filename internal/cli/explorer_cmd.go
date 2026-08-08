package cli

import (
	"fmt"
	"os"

	"codeberg.org/kuchmenko/workspace/internal/agent"
	"codeberg.org/kuchmenko/workspace/internal/tui"
	"github.com/spf13/cobra"
)

func newExplorerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "explorer",
		Aliases: []string{"agent"},
		Short:   "TUI explorer for projects, worktrees, and Claude sessions",
		Annotations: map[string]string{
			"capability":   "explorer",
			"agent:when":   "Browse workspaces, projects, and worktrees, then launch or resume Claude Code sessions",
			"agent:safety": "Interactive TUI. Use subcommands (launch, shell, resume) for non-interactive access.",
		},
		Long: `Launch the interactive TUI explorer over every registered workspace.
The pinned quick-nav header shows up to nine numbered chips (favorites
+ recently-touched) — press 1-9 to open the matching project. Below
the header, switch between Recent, Projects, and Language views.

Navigation: j/k to move, Enter to open, h/Esc to go back, q to quit.
Use v to change view, o to reverse recency, S to search projects and
worktrees globally, and A for lifecycle maintenance. Subcommands provide non-interactive
access to the same actions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplorerTUI()
		},
	}
	cmd.AddCommand(
		newExplorerLaunchCmd(),
		newExplorerShellCmd(),
		newExplorerResumeCmd(),
	)
	return cmd
}

func newExplorerLaunchCmd() *cobra.Command {
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
			stampLaunchActivity(args[0])
			return agent.LaunchClaude(args[0], "", prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "initial prompt for claude")
	return cmd
}

func newExplorerShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <path>",
		Short: "Open shell in a directory (non-interactive)",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Open a new shell in a specific project directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stampLaunchActivity(args[0])
			return agent.LaunchShell(args[0])
		},
	}
}

func newExplorerResumeCmd() *cobra.Command {
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
			stampLaunchActivity(session.Cwd)
			return agent.LaunchClaude(session.Cwd, session.ID, prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "additional prompt for the resumed session")
	return cmd
}

func runExplorerTUI() error {
	cwd, _ := os.Getwd()
	workspaces, sessCache, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	m := agent.NewModel(workspaces, sessCache)
	p := tui.NewProgram(m, tui.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if final, ok := finalModel.(*agent.Model); ok && final.Launch != nil {
		stampLaunchActivity(final.Launch.Cwd)
		if final.Launch.ShellOnly {
			return agent.LaunchShell(final.Launch.Cwd)
		}
		return agent.LaunchClaude(final.Launch.Cwd, final.Launch.ResumeID, final.Launch.Prompt)
	}
	return nil
}

func stampLaunchActivity(cwd string) {
	if err := agent.StampLaunchFromPath(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "ws agent: stamp activity: %v\n", err)
	}
}
