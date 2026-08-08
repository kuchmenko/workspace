package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/tui"
	"github.com/spf13/cobra"
)

func newExplorerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explorer",
		Short: "TUI explorer for projects and worktrees",
		Long: `Launch the interactive TUI explorer over every registered workspace.
The pinned quick-nav header shows up to nine numbered chips (favorites
+ recently-touched) — press 1-9 to launch the matching project. Below
the header, cycle Recent, Projects, and Language views with v.

Navigation: j/k move, g/G jump, Ctrl+D/U move half a page, and Ctrl+F/B
or PageDown/PageUp move a page. Enter or l opens; h or Esc goes back; q quits.
Use o to reverse Recent order, S for global project/worktree search,
Ctrl+S opens a shell, and A opens lifecycle maintenance. 1-9 launches a chip. Subcommands provide non-interactive
access to the same actions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplorerTUI()
		},
	}
	cmd.AddCommand(newExplorerShellCmd())
	return cmd
}

func newExplorerShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <path>",
		Short: "Open shell in a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stampLaunchActivity(args[0])
			return agent.LaunchShell(args[0])
		},
	}
}

func runExplorerTUI() error {
	metrics.RecordExplorerInvoked()
	cwd, _ := os.Getwd()
	workspaces, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws explorer: %s\n", d)
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
		fmt.Fprintf(os.Stderr, "ws explorer: stamp activity: %v\n", err)
	}
}
