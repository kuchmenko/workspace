package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	var template string
	var dumpJSON bool
	var graphics bool
	var gpScale float64
	var gpZoom float64
	var bench int
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Canvas TUI launcher for Claude Code sessions across workspaces",
		Long: `Launch a canvas-based TUI that lets you navigate workspaces, projects,
and worktrees as a deterministic graph and start (or resume) Claude Code
sessions in any of them.

Pass --template <name> to load a synthetic fixture instead of real data,
useful for visually validating layout and rendering. Available templates:
` + "  " + agentTemplateList() + `

Pass --json to dump the laid-out graph + slot/highlight state as JSON
and exit. Useful for debugging layout issues without a TTY.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTUI(template, dumpJSON, graphics, gpScale, gpZoom, bench)
		},
	}
	cmd.Flags().StringVar(&template, "template", "", "load synthetic graph fixture by name (see --help)")
	cmd.Flags().BoolVar(&dumpJSON, "json", false, "dump layout state as JSON and exit")
	cmd.Flags().BoolVar(&graphics, "graphics", false, "interactive graphics mode via Kitty graphics protocol")
	cmd.Flags().Float64Var(&gpScale, "scale", 0.5, "render resolution scale for --graphics (0.25=fast, 0.5=balanced, 1.0=native)")
	cmd.Flags().Float64Var(&gpZoom, "zoom", 1.0, "camera zoom for --graphics (0.3=see whole graph, 1.0=default, 2.0=close-up). Also +/- at runtime")
	cmd.Flags().IntVar(&bench, "bench", 0, "headless benchmark: render N frames, print per-stage timing, exit")
	return cmd
}

func agentTemplateList() string {
	names := agent.TemplateNames()
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

// runAgentTUI starts the bubbletea program. If template is non-empty,
// the graph is loaded from a synthetic fixture; otherwise we pull
// workspaces from daemon.toml (with fallback to cwd workspace.toml).
//
// The cross-renderer model has no global layout — slot positions are
// computed per focused node at render time, so there is no slot store
// to persist.
func runAgentTUI(template string, dumpJSON, graphics bool, gpScale, gpZoom float64, bench int) error {
	var g *agent.Graph

	if template != "" {
		var err error
		g, err = agent.LoadTemplate(template)
		if err != nil {
			return err
		}
	} else {
		cwd, _ := os.Getwd()
		var diagnostics []string
		g, diagnostics = agent.BuildGraph(cwd)
		for _, d := range diagnostics {
			fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
		}
	}

	// Cross-aware grid embedding: each parent's top children land at +1
	// cardinal offsets, so when the camera centers on focused they
	// appear in the cross positions naturally.
	agent.GlobalLayout(g)

	m := agent.NewModel(g)

	if dumpJSON {
		data, err := m.MarshalDump()
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if bench > 0 {
		return agent.RunBench(g, gpScale, gpZoom, bench)
	}

	if graphics {
		return agent.RunGraphicsMode(g, gpScale, gpZoom)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
