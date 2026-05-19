package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/spf13/cobra"
)

func newBootstrapCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "bootstrap [project]",
		Short: "Clone projects from workspace.toml that are missing on this machine",
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "On a fresh machine, clone all projects listed in workspace.toml directly into the bare+worktree layout",
		},
		Long: `Materialize projects listed in workspace.toml into the bare+worktree
layout. On a fresh machine where workspace.toml has been pulled but nothing
is cloned yet, 'ws bootstrap' walks the registry and clones each missing
project directly into the canonical layout.

Bootstrap is interactive: it shows a plan of what will be done, prompts for
the default branch when it cannot be auto-detected, and surfaces any
errors before continuing.

Bootstrap is crash-safe via a sidecar progress file at
~/.local/state/ws/bootstrap/. While bootstrap is running, the daemon pauses
all sync activity for that workspace to avoid races and half-pushed state.

Examples:
  ws bootstrap                clone every active project missing locally
  ws bootstrap myapp          clone one specific project
  ws bootstrap --dry-run      show plan without cloning`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(args, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show plan without cloning")
	return cmd
}

func runBootstrap(args []string, dryRun bool) error {
	plan := bootstrap.ScanPlan(wsRoot, ws, args)
	if len(plan.Items) == 0 {
		fmt.Println("No active projects to bootstrap.")
		return nil
	}

	existing, err := bootstrap.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("read sidecar: %w", err)
	}
	resumeFrom := map[string]bootstrap.DoneEntry{}
	if existing != nil {
		if bootstrap.IsAlive(existing) {
			return fmt.Errorf("bootstrap already running (pid %d, started %s)",
				existing.Meta.PID, existing.Meta.Started.Local().Format(time.RFC3339))
		}

		fmt.Printf("Found incomplete bootstrap from %s (pid %d, %d projects done).\n",
			existing.Meta.Started.Local().Format(time.RFC3339),
			existing.Meta.PID, len(existing.Done))
		fmt.Print("Resume? [Y/n/discard]: ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "", "y", "yes":
			resumeFrom, err = existing.DoneEntries()
			if err != nil {
				return fmt.Errorf("read sidecar entries: %w", err)
			}
		case "d", "discard":
			if err := bootstrap.Delete(wsRoot); err != nil {
				return err
			}
		default:
			fmt.Println("Aborted.")
			return nil
		}
	}

	if dryRun {
		printPlanText(plan)
		return nil
	}

	toClone := []bootstrap.PlanItem{}
	for _, it := range plan.Bucket(bootstrap.StateMissing) {
		if _, done := resumeFrom[it.Name]; done {
			continue
		}
		toClone = append(toClone, it)
	}
	if len(toClone) == 0 && len(resumeFrom) == 0 {
		printPlanText(plan)
		fmt.Println("Nothing to clone.")
		return nil
	}

	model := newBootstrapModel(plan, toClone, resumeFrom)
	p := tea.NewProgram(model, tea.WithAltScreen())
	program = p
	defer func() { program = nil }()
	finalRaw, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("TUI crashed: %w", runErr)
	}
	final := finalRaw.(bootstrapModel)

	if final.canceled {
		fmt.Println("Bootstrap canceled by user.")
		return nil
	}

	if len(final.errors) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, errorBannerStyle.Render("Bootstrap finished with errors:"))
		for _, e := range final.errors {
			fmt.Fprintf(os.Stderr, "\n  %s\n", e.project)
			fmt.Fprintln(os.Stderr, indent(strings.TrimSpace(e.err.Error()), "    "))
		}
	}

	if final.sidecar != nil && len(final.sidecar.Done) > 0 {
		if err := commitBootstrap(final.sidecar); err != nil {
			return fmt.Errorf("commit bootstrap: %w", err)
		}

		if err := bootstrap.Delete(wsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove sidecar: %v\n", err)
		}
	}

	cloned := len(final.successes)
	failed := len(final.errors)
	total := cloned + failed
	fmt.Printf("\nBootstrap complete: %d cloned, %d failed (of %d planned).\n", cloned, failed, total)
	if failed > 0 {
		conflict.Notify("ws: bootstrap finished with errors",
			fmt.Sprintf("%d/%d cloned — see terminal", cloned, total))
	} else if cloned > 0 {
		conflict.Notify("ws: bootstrap finished",
			fmt.Sprintf("%d projects cloned", cloned))
	}

	if failed > 0 {
		return errors.New("bootstrap finished with errors")
	}
	return nil
}

func commitBootstrap(sc *bootstrap.Sidecar) error {
	freshWS, err := config.Load(wsRoot)
	if err != nil {
		return err
	}
	entries, err := sc.DoneEntries()
	if err != nil {
		return err
	}
	for name, entry := range entries {
		proj, ok := freshWS.Projects[name]
		if !ok {
			continue
		}
		if proj.DefaultBranch == "" && entry.DefaultBranch != "" {
			proj.DefaultBranch = entry.DefaultBranch
			freshWS.Projects[name] = proj
		}
	}

	ws = freshWS
	return saveWorkspace()
}

func printPlanText(plan *bootstrap.Plan) {
	fmt.Println("Bootstrap plan:")
	for _, s := range []bootstrap.State{
		bootstrap.StateMissing,
		bootstrap.StatePresent,
		bootstrap.StateNeedsMigrate,
		bootstrap.StateBlocked,
		bootstrap.StateSelf,
	} {
		items := plan.Bucket(s)
		if len(items) == 0 {
			continue
		}
		fmt.Printf("  %s (%d)\n", s, len(items))
		for _, it := range items {
			if it.Reason != "" {
				fmt.Printf("    - %-30s %s\n", it.Name, it.Reason)
			} else {
				fmt.Printf("    - %s\n", it.Name)
			}
		}
	}
}
