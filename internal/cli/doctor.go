package cli

import (
	"fmt"
	"os"

	"github.com/kuchmenko/workspace/internal/doctor"
	"github.com/spf13/cobra"
)

// Exit codes. Documented in the --help text so the acceptance criteria
// is self-describing and scriptable.
const (
	exitDoctorOK        = 0
	exitDoctorIssues    = 1
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

Runs system-level checks (daemon, stale sidecars, active conflicts,
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
		Annotations: map[string]string{
			"capability":    "observability",
			"agent:when":    "Diagnose workspace health; surface missing refspecs, stale sidecars, conflicts, config issues.",
			"agent:safety":  "Read-only unless --fix is set. --fix only applies safe, idempotent mutations (refspec, remote URL, branch upstream, default_branch, stale sidecars).",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
				if _, ok := ws.Projects[only]; !ok {
					return fmt.Errorf("unknown project %q", only)
				}
			}

			r := &doctor.Runner{
				WsRoot:     wsRoot,
				WS:         ws,
				Only:       only,
				SkipRemote: skipRemote,
			}
			report := r.Run()

			var fixesApplied int
			if fix {
				fixesApplied = doctor.ApplyFixes(report)
			}

			if asJSON {
				if err := doctor.WriteJSON(os.Stdout, report); err != nil {
					return err
				}
			} else {
				doctor.WriteText(os.Stdout, report)
			}

			os.Exit(exitCodeFor(report, fix, fixesApplied))
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "apply all safe auto-fixes in batch (no prompts)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON instead of text")
	cmd.Flags().BoolVar(&skipRemote, "skip-remote", false, "skip network-touching checks (remote reachability)")

	return cmd
}

// exitCodeFor maps a (report, flags) pair to the documented exit code.
// The scheme is:
//
//   - --fix ran AND at least one fix succeeded → 2 (state changed).
//   - any warn/error present in the final report → 1.
//   - otherwise → 0.
//
// Note that "fix succeeded but issues remain" still returns 2 — the user
// asked for --fix, we applied what we could, and the shell exit code
// should reflect that state mutation happened.
func exitCodeFor(rep *doctor.Report, fixRequested bool, fixesApplied int) int {
	if fixRequested && fixesApplied > 0 {
		return exitDoctorFixApplied
	}
	if rep.MaxSeverity() >= doctor.Warn {
		return exitDoctorIssues
	}
	return exitDoctorOK
}
