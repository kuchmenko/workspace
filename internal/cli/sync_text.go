package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	workspacesync "github.com/kuchmenko/workspace/internal/sync"
	"golang.org/x/term"
)

const (
	syncExitFailed   = 1
	syncExitCanceled = 130
)

type ExitError struct {
	Code int
}

func (e ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func runSync(parent context.Context, root string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := config.EnsureLegacyDaemonStopped(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fresh, err := config.Load(root)
	if err != nil {
		return err
	}
	plan := workspacesync.BuildPlan(root, fresh)
	if syncTerminal(stdin) && syncTerminal(stdout) {
		return runSyncTUI(ctx, root, plan, stdout)
	}
	return runSyncHeadless(ctx, root, plan, stdout, stderr)
}

func syncTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func runSyncHeadless(ctx context.Context, root string, plan workspacesync.Plan, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "preflight: %d endpoint(s)\n", len(plan.Endpoints))
	probes := workspacesync.Probe(ctx, plan, nil)
	failed := 0
	for index, result := range probes.Results {
		endpoint := plan.Endpoints[index]
		fmt.Fprintf(stdout, "preflight %s %s\n", result.Status, git.RedactRemote(result.URL))
		if !endpoint.Executable || result.Status != workspacesync.ProbeSuccess {
			failed++
			if result.Diagnostic != "" {
				fmt.Fprintf(stderr, "preflight %s: %s\n", git.RedactRemote(result.URL), result.Diagnostic)
			}
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stdout, "summary: canceled during preflight")
		return ExitError{Code: syncExitCanceled}
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "summary: preflight failed=%d; no changes made\n", failed)
		return ExitError{Code: syncExitFailed}
	}

	selection := workspacesync.NewSelection(plan, probes)
	runner := workspacesync.NewRunner(root, log.New(io.Discard, "", 0))
	report := runner.RunContext(ctx, selection, func(event workspacesync.Event) {
		writeSyncEvent(stdout, event)
	})
	writeSyncSummary(stdout, report)
	code := classifySyncReport(report)
	if code != 0 {
		return ExitError{Code: code}
	}
	return nil
}

func writeSyncEvent(output io.Writer, event workspacesync.Event) {
	label := event.Operation
	if event.Project != "" {
		label += " " + event.Project
	}
	if event.Mirror != "" {
		label += "/" + event.Mirror
	}
	if event.Kind == workspacesync.EventStarted {
		fmt.Fprintf(output, "start: %s\n", label)
		return
	}
	if event.Kind == workspacesync.EventConflict {
		fmt.Fprintf(output, "conflict: %s %s\n", label, event.Diagnostic)
		return
	}
	if event.Status != "" {
		fmt.Fprintf(output, "result: %s %s\n", event.Status, label)
	}
}

type syncCounts struct {
	success  int
	failed   int
	skipped  int
	canceled int
}

func writeSyncSummary(output io.Writer, report workspacesync.Report) {
	counts := countSyncReport(report)
	fmt.Fprintf(output, "summary: success=%d failed=%d skipped=%d canceled=%d conflicts=%d\n", counts.success, counts.failed, counts.skipped, counts.canceled, len(report.Conflicts))
}

func writeSyncInteractiveSummary(output io.Writer, report workspacesync.Report) {
	counts := countSyncReport(report)
	status := "completed"
	if report.Canceled {
		status = "canceled"
	} else if classifySyncReport(report) != 0 {
		status = "completed with issues"
	}
	fmt.Fprintf(output, "Sync %s\n\n", status)
	fmt.Fprintf(output, "  success:     %d\n", counts.success)
	fmt.Fprintf(output, "  failed:      %d\n", counts.failed)
	fmt.Fprintf(output, "  skipped:     %d\n", counts.skipped)
	fmt.Fprintf(output, "  canceled:    %d\n", counts.canceled)
	fmt.Fprintf(output, "  conflicts:   %d\n", len(report.Conflicts))
	fmt.Fprintf(output, "  conversions: %d\n", len(report.Conversions))
}

func countSyncReport(report workspacesync.Report) syncCounts {
	counts := syncCounts{}
	results := append([]workspacesync.OperationResult{}, report.Workspace...)
	results = append(results, report.Projects...)
	results = append(results, report.Mirrors...)
	for _, result := range results {
		counts.add(result.Status)
	}
	for _, conversion := range report.Conversions {
		counts.add(conversion.Status)
	}
	return counts
}

func (c *syncCounts) add(status workspacesync.ResultStatus) {
	switch status {
	case workspacesync.ResultSuccess:
		c.success++
	case workspacesync.ResultFailed:
		c.failed++
	case workspacesync.ResultSkipped:
		c.skipped++
	case workspacesync.ResultCanceled:
		c.canceled++
	}
}

func classifySyncReport(report workspacesync.Report) int {
	if report.Canceled {
		return syncExitCanceled
	}
	if len(report.Conflicts) > 0 {
		return syncExitFailed
	}
	for _, result := range append(append(append([]workspacesync.OperationResult{}, report.Workspace...), report.Projects...), report.Mirrors...) {
		if result.Status == workspacesync.ResultFailed || result.Status == workspacesync.ResultCanceled {
			return syncExitFailed
		}
		if result.Status == workspacesync.ResultSkipped && result.Reason != workspacesync.SkipExcluded {
			return syncExitFailed
		}
	}
	for _, conversion := range report.Conversions {
		if conversion.Status != workspacesync.ResultSuccess {
			return syncExitFailed
		}
	}
	return 0
}
