package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/spf13/cobra"
)

const (
	exitDoctorOK         = 0
	exitDoctorIssues     = 1
	exitDoctorFixApplied = 2
)

func newDoctorCmd() *cobra.Command {
	var (
		fix        bool
		asJSON     bool
		skipRemote bool
	)

	cmd := &cobra.Command{
		Use:   "doctor [project]",
		Short: "Diagnose the workspace — system + per-project health checks",
		Long: `Diagnose the workspace.

Runs system-level checks (stale sidecars, active conflicts,
config validity) followed by per-project checks (layout, fetch refspec,
remote URL, reachability, default branch, branch upstream, index locks).

Exit codes:
  0  all checks passed
  1  one or more issues found
  2  --fix applied at least one auto-fix

With --fix, every finding that advertises an auto-fix is applied in
batch (no prompts). Fixes that require judgement — resolving conflicts,
clearing index.lock — are never auto-applied; the report prints a hint
and leaves the action to the user.`,
		Annotations: agentAnnotations("diagnose", AgentInteractionNone, AgentApprovalConditional, AgentEffectConditional, AgentEffectRead, "text-or-json", "0,1,2"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			metrics.RecordDoctorInvoked()
			only := ""
			if len(args) == 1 {
				only = args[0]
				if ws != nil {
					if _, ok := ws.Projects[only]; !ok {
						return fmt.Errorf("unknown project %q", only)
					}
				}
			}

			r := &Runner{
				WsRoot:        wsRoot,
				WS:            ws,
				ConfigLoadErr: wsLoadErr,
				Only:          only,
				SkipRemote:    skipRemote,
			}

			streaming := !asJSON && !fix
			if streaming {
				first := true
				r.OnScope = func(scope string, findings []Finding) {
					WriteScope(os.Stdout, scope, findings, first)
					first = false
				}
			}
			report := r.Run(cmd.Context())
			metrics.RecordDoctorActionableFound(FixableCount(report))

			var fixesApplied int
			if fix {
				fixesApplied = ApplyFixes(report)
				metrics.RecordDoctorFixApplied(fixesApplied)
			}

			switch {
			case asJSON:
				if err := WriteJSON(os.Stdout, report); err != nil {
					return err
				}
			case streaming:
				WriteFooter(os.Stdout, report, FixableCount(report))
			default:
				WriteText(os.Stdout, report)
			}

			code := exitCodeFor(report, fix, fixesApplied)
			if code != exitDoctorOK {
				return ExitError{Code: code}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "apply all safe auto-fixes in batch (no prompts)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON instead of text")
	cmd.Flags().BoolVar(&skipRemote, "skip-remote", false, "skip network-touching checks (remote reachability)")

	return cmd
}

func exitCodeFor(rep *Report, fixRequested bool, fixesApplied int) int {
	if fixRequested && fixesApplied > 0 {
		return exitDoctorFixApplied
	}
	if rep.MaxSeverity() >= Warn {
		return exitDoctorIssues
	}
	return exitDoctorOK
}
