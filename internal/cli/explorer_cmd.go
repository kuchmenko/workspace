package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/kuchmenko/workspace/internal/tui"
	"github.com/spf13/cobra"
)

func newExplorerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "explorer",
		Aliases: []string{"agent"},
		Short:   "TUI explorer for projects and worktrees",
		Annotations: map[string]string{
			"capability":   "explorer",
			"agent:when":   "Browse workspaces, projects, and worktrees, then open a shell",
			"agent:safety": "Interactive TUI. Use the shell subcommand for non-interactive access.",
		},
		Long: `Launch the interactive TUI explorer over every registered workspace.
The pinned quick-nav header shows up to nine numbered chips (favorites
+ recently-touched) — press 1-9 to launch the matching project. Below
the header, the full project tree scrolls with j/k navigation.

Navigation: j/k to move, Enter to open, h/Esc to go back, q to quit.
1-9 to launch a chip directly. Subcommands provide non-interactive
access to the same actions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplorerTUI()
		},
	}
	cmd.AddCommand(newExplorerShellCmd())
	return cmd
}

func newExplorerShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "shell <path>",
		Aliases: []string{"launch"},
		Short:   "Open shell in a directory (non-interactive)",
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

func runExplorerTUI() error {
	cwd, _ := os.Getwd()
	workspaces, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	m := agent.NewModel(workspaces)
	p := tui.NewProgram(m, tui.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if final, ok := finalModel.(*agent.Model); ok && final.Launch != nil {
		stampLaunchActivity(final.Launch.Cwd)
		return agent.LaunchShell(final.Launch.Cwd)
	}
	return nil
}

func stampLaunchActivity(cwd string) {
	if err := agent.StampLaunchFromPath(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "ws agent: stamp activity: %v\n", err)
	}
}
