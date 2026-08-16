package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	workspacesync "github.com/kuchmenko/workspace/internal/sync"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestRunSyncHeadlessFailedPreflightDoesNotMutate(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	missingRemote := filepath.Join(t.TempDir(), "missing.git")
	workspace := &config.Workspace{
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{},
		Projects: map[string]config.Project{
			"app": {
				Remote:        missingRemote,
				Path:          "personal/app",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
			},
		},
	}
	registerSyncTestWorkspace(t, root, workspace)
	plan := workspacesync.BuildPlan(root, workspace)
	var stdout, stderr bytes.Buffer
	err := runSyncHeadless(context.Background(), root, plan, &stdout, &stderr)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != syncExitFailed {
		t.Fatalf("error = %v, want exit %d", err, syncExitFailed)
	}
	if _, err := os.Stat(filepath.Join(root, "personal/app")); !os.IsNotExist(err) {
		t.Fatalf("project path was mutated: %v", err)
	}
	output := stdout.String() + stderr.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("headless output contains ANSI: %q", output)
	}
	if !strings.Contains(stdout.String(), "no project changes made") {
		t.Fatalf("missing no-mutation summary: %s", stdout.String())
	}
}

func TestRunSyncHeadlessExecutesAllAfterSuccessfulPreflight(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projectRemote := testutil.InitFakeRemote(t, "app", "main")
	root := t.TempDir()
	workspace := &config.Workspace{
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{},
		Projects: map[string]config.Project{
			"app": {
				Remote:        projectRemote,
				Path:          "personal/app",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
			},
		},
	}
	registerSyncTestWorkspace(t, root, workspace)
	plan := workspacesync.BuildPlan(root, workspace)
	var stdout, stderr bytes.Buffer
	if err := runSyncHeadless(context.Background(), root, plan, &stdout, &stderr); err != nil {
		t.Fatalf("runSyncHeadless: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	mainPath := filepath.Join(root, "personal/app")
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("main worktree was not created: %v", err)
	}
	if _, err := os.Stat(layout.BarePath(mainPath)); err != nil {
		t.Fatalf("bare repository was not created: %v", err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "\x1b[") {
		t.Fatal("headless output contains ANSI")
	}
	if strings.Contains(stdout.String(), "start: workspace-sync") || !strings.Contains(stdout.String(), "start: project-sync app") {
		t.Fatalf("missing execution progress:\n%s", stdout.String())
	}
}

func TestRunSyncWithoutDeviceNetworkSynchronizesProjects(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projectRemote := testutil.InitFakeRemote(t, "app", "main")
	root := t.TempDir()
	workspace := &config.Workspace{
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{},
		Projects: map[string]config.Project{
			"app": {
				Remote:        projectRemote,
				Path:          "personal/app",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
			},
		},
	}
	registerSyncTestWorkspace(t, root, workspace)
	var stdout, stderr bytes.Buffer
	if err := runSync(context.Background(), root, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runSync: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !git.IsRepo(filepath.Join(root, "personal/app")) {
		t.Fatal("project was not synchronized")
	}
	if strings.Contains(stdout.String(), "workspace-sync:") || stderr.Len() != 0 {
		t.Fatalf("unexpected local workspace output:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunSyncCanceledBeforeInitialExchangeReturns130(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	registerSyncTestWorkspace(t, root, &config.Workspace{Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	err := runSync(ctx, root, strings.NewReader(""), &stdout, &stderr)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != syncExitCanceled {
		t.Fatalf("error = %v, want exit %d", err, syncExitCanceled)
	}
}

func registerSyncTestWorkspace(t *testing.T, root string, workspace *config.Workspace) {
	t.Helper()
	workspace.Meta.Version = 1
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), filepath.Base(root), root, workspace); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

func TestClassifySyncReport(t *testing.T) {
	tests := []struct {
		name   string
		report workspacesync.Report
		code   int
	}{
		{name: "success", report: workspacesync.Report{Projects: []workspacesync.OperationResult{{Status: workspacesync.ResultSuccess}}}},
		{name: "deliberate exclusion", report: workspacesync.Report{Projects: []workspacesync.OperationResult{{Status: workspacesync.ResultSkipped, Reason: workspacesync.SkipExcluded}}}},
		{name: "failed", report: workspacesync.Report{Projects: []workspacesync.OperationResult{{Status: workspacesync.ResultFailed}}}, code: syncExitFailed},
		{name: "conflict", report: workspacesync.Report{Conflicts: []workspacesync.OperationResult{{Status: workspacesync.ResultFailed}}}, code: syncExitFailed},
		{name: "non-exclusion skip", report: workspacesync.Report{Projects: []workspacesync.OperationResult{{Status: workspacesync.ResultSkipped, Reason: workspacesync.SkipPlanChanged}}}, code: syncExitFailed},
		{name: "canceled", report: workspacesync.Report{Canceled: true}, code: syncExitCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code := classifySyncReport(test.report); code != test.code {
				t.Fatalf("code = %d, want %d", code, test.code)
			}
		})
	}
}

func TestWriteSyncSummaryIsPlainAndClassified(t *testing.T) {
	report := workspacesync.Report{
		Workspace: []workspacesync.OperationResult{{Status: workspacesync.ResultSuccess}},
		Projects:  []workspacesync.OperationResult{{Status: workspacesync.ResultSkipped, Reason: workspacesync.SkipExcluded}},
	}
	var output bytes.Buffer
	writeSyncSummary(&output, report)
	if got := output.String(); got != "summary: success=1 failed=0 skipped=1 canceled=0 conflicts=0\n" {
		t.Fatalf("summary = %q", got)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatal("summary contains ANSI")
	}
}

func TestWriteTopLevelWorkspaceSyncContinuesOfflineAndRejectsInvalidHistory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	results := []peernetwork.SyncResult{{Device: "asahi", Status: "unavailable"}}
	if err := writeTopLevelWorkspaceSync(&stdout, &stderr, results, []string{"shared/asahi: unavailable"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "workspace-sync: asahi unavailable\n" || !strings.Contains(stderr.String(), "continuing offline") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	results = []peernetwork.SyncResult{{Device: "asahi", Status: "rejected"}}
	err := writeTopLevelWorkspaceSync(&stdout, &stderr, results, []string{"shared/asahi: workspace epoch is stale"})
	if err == nil || err.Error() != "shared/asahi: workspace epoch is stale" {
		t.Fatalf("error = %v", err)
	}
}
