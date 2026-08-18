package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/metrics"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	workspacesync "github.com/kuchmenko/workspace/internal/sync"
	"golang.org/x/term"
)

const (
	syncExitFailed   = 1
	syncExitCanceled = 130
)

func runSync(parent context.Context, root string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	current, err := synchronizeCurrentWorkspace(ctx, root, stdout, stderr)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ExitError{Code: syncExitCanceled}
		}
		return err
	}
	plan := workspacesync.BuildPlan(root, current.State)
	var runErr error
	if syncTerminal(stdin) && syncTerminal(stdout) {
		runErr = runSyncTUI(ctx, root, plan, stdout)
	} else {
		runErr = runSyncHeadless(ctx, root, plan, stdout, stderr)
	}
	var exitErr ExitError
	if errors.As(runErr, &exitErr) && exitErr.Code == syncExitCanceled || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return ExitError{Code: syncExitCanceled}
	}
	_, publishErr := synchronizeCurrentWorkspace(ctx, root, stdout, stderr)
	if runErr != nil {
		if publishErr != nil {
			fmt.Fprintf(stderr, "workspace-sync: %v\n", publishErr)
		}
		return runErr
	}
	if errors.Is(publishErr, context.Canceled) || errors.Is(publishErr, context.DeadlineExceeded) {
		return ExitError{Code: syncExitCanceled}
	}
	return publishErr
}

func synchronizeCurrentWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (registry.Workspace, error) {
	store, identity, err := openNetworkNode()
	if err != nil {
		return registry.Workspace{}, err
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(ctx, root)
	if err != nil {
		return registry.Workspace{}, err
	}
	if _, err = store.Network(ctx); errors.Is(err, sql.ErrNoRows) {
		return requireResolvedWorkspace(ctx, store, workspace)
	} else if err != nil {
		return registry.Workspace{}, err
	}
	name, err := networkDeviceName("")
	if err != nil {
		return registry.Workspace{}, err
	}
	peers, err := peernetwork.DiscoverPeers(ctx, store, identity, name, 1500*time.Millisecond)
	if err != nil {
		return registry.Workspace{}, err
	}
	results, failures, err := synchronizeWorkspacePeersContext(ctx, store, identity, name, []registry.Workspace{workspace}, peers)
	if err != nil {
		return registry.Workspace{}, err
	}
	if err = writeTopLevelWorkspaceSync(stdout, stderr, results, failures); err != nil {
		return registry.Workspace{}, err
	}
	workspace, err = reloadSynchronizedWorkspace(ctx, store, root, stderr)
	if err != nil {
		return registry.Workspace{}, err
	}
	return requireResolvedWorkspace(ctx, store, workspace)
}

func reloadSynchronizedWorkspace(ctx context.Context, store *registry.Store, root string, stderr io.Writer) (registry.Workspace, error) {
	workspace, err := store.LoadByRoot(ctx, root)
	if err != nil {
		return registry.Workspace{}, err
	}
	if err = alias.WriteStateFile(workspace.State, workspace.Root); err != nil {
		fmt.Fprintf(stderr, "warning: could not update alias state file: %v\n", err)
	} else {
		metrics.RecordAliasStateGenerated()
	}
	return workspace, nil
}

func requireResolvedWorkspace(ctx context.Context, store *registry.Store, workspace registry.Workspace) (registry.Workspace, error) {
	conflicted, err := store.HasUnresolvedConflicts(ctx, workspace.Name)
	if err != nil {
		return registry.Workspace{}, err
	}
	if conflicted {
		return registry.Workspace{}, fmt.Errorf("workspace %s has unresolved registry conflicts", workspace.Name)
	}
	return workspace, nil
}

func writeTopLevelWorkspaceSync(stdout, stderr io.Writer, results []peernetwork.SyncResult, failures []string) error {
	failureIndex := 0
	for _, result := range results {
		device := terminalText(result.Device)
		status := terminalText(result.Status)
		fmt.Fprintf(stdout, "workspace-sync: %s %s\n", device, status)
		switch result.Status {
		case "unavailable":
			fmt.Fprintf(stderr, "workspace-sync: %s is unavailable; continuing offline\n", device)
		case "rejected":
			if failureIndex < len(failures) {
				return errors.New(terminalText(failures[failureIndex]))
			}
			return fmt.Errorf("workspace sync with %s was rejected", device)
		case "conflicted":
			return fmt.Errorf("workspace sync with %s has unresolved conflicts", device)
		}
		if result.Status == "unavailable" || result.Status == "rejected" {
			failureIndex++
		}
	}
	return nil
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
		fmt.Fprintf(stdout, "summary: preflight failed=%d; no project changes made\n", failed)
		return ExitError{Code: syncExitFailed}
	}

	selection := workspacesync.NewSelection(plan, probes)
	runner := workspacesync.NewRunner(root, log.New(io.Discard, "", 0))
	notifier := newSyncConflictNotifier(root)
	report := runner.RunContext(ctx, selection, func(event workspacesync.Event) {
		notifier.notifyNew()
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
		label += " " + terminalText(event.Project)
	}
	if event.Mirror != "" {
		label += "/" + terminalText(event.Mirror)
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
