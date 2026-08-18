package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
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

func TestRunSyncHeadlessFailedPreflightDoesNotExchangeWorkspace(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := t.TempDir()
	registerSyncTestWorkspace(t, root, &config.Workspace{
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{"app": "personal/app"},
		Projects: map[string]config.Project{
			"app": {
				Remote:        filepath.Join(t.TempDir(), "missing.git"),
				Path:          "personal/app",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
			},
		},
	})
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnsureNetwork(context.Background(), "local"); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	aliasPath, err := alias.StateFilePath()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = runSync(context.Background(), root, strings.NewReader(""), &stdout, &stderr)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != syncExitFailed {
		t.Fatalf("error = %v, want exit %d", err, syncExitFailed)
	}
	if _, err = os.Stat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("workspace exchange generated alias state before preflight succeeded: %v", err)
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

func TestWriteSyncEventEscapesProjectAndMirrorControls(t *testing.T) {
	var output bytes.Buffer
	writeSyncEvent(&output, workspacesync.Event{
		Kind:      workspacesync.EventMirror,
		Status:    workspacesync.ResultSuccess,
		Operation: "mirror-push",
		Project:   "app\n\x1b]8;;https://example.com\x07",
		Mirror:    "backup\u009dunsafe",
	})

	got := output.String()
	if strings.ContainsAny(got, "\x1b\u009d") || strings.Count(got, "\n") != 1 {
		t.Fatalf("sync event contains project or mirror controls: %q", got)
	}
	if !strings.Contains(got, `app\x0A\x1B]8;;https://example.com\x07/backup\x9Dunsafe`) {
		t.Fatalf("sync event did not escape project and mirror controls: %q", got)
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

func TestRunSyncStopsBeforeProjectsWithLocalWorkspaceConflictAndNoReachablePeer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, projectPath := createSyncTestWorkspaceConflict(t, false)

	var stdout, stderr bytes.Buffer
	err := runSync(context.Background(), root, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unresolved registry conflicts") {
		t.Fatalf("runSync error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(projectPath); !os.IsNotExist(statErr) {
		t.Fatalf("project sync started with unresolved workspace conflict: %v", statErr)
	}
}

func TestRunSyncStopsBeforeProjectsWithLocalWorkspaceAccessConflictAndNoReachablePeer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, projectPath := createSyncTestWorkspaceConflict(t, true)

	var stdout, stderr bytes.Buffer
	err := runSync(context.Background(), root, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unresolved registry conflicts") {
		t.Fatalf("runSync error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(projectPath); !os.IsNotExist(statErr) {
		t.Fatalf("project sync started with unresolved workspace access conflict: %v", statErr)
	}
}

func createSyncTestWorkspaceConflict(t *testing.T, access bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	projectRemote := testutil.InitFakeRemote(t, "app", "main")
	root := t.TempDir()
	state := &config.Workspace{
		Meta:    config.Meta{Version: 1},
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{"editor": "vim"},
		Projects: map[string]config.Project{"app": {
			Remote: projectRemote, Path: "personal/app", Status: config.StatusActive,
			Category: config.CategoryPersonal, DefaultBranch: "main",
		}},
	}
	left, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	rightPath := filepath.Join(t.TempDir(), "registry.db")
	right, err := registry.Open(rightPath)
	if err != nil {
		t.Fatal(err)
	}
	if access {
		leftIdentity, loadErr := device.Load(filepath.Join(filepath.Dir(mustRegistryPath(t)), "identity.key"))
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		rightIdentity, loadErr := device.Load(filepath.Join(filepath.Dir(rightPath), "identity.key"))
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if _, err = left.EnsureNetwork(ctx, "left"); err != nil {
			t.Fatal(err)
		}
		if _, err = left.AddNetworkDevice(ctx, "right", rightIdentity.PublicKey(), registry.NetworkAdmin); err != nil {
			t.Fatal(err)
		}
		network, exportErr := left.ExportNetwork(ctx)
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		if _, err = right.ImportNetwork(ctx, network, leftIdentity.ID()); err != nil {
			t.Fatal(err)
		}
		if _, err = left.Create(ctx, filepath.Base(root), root, state); err != nil {
			t.Fatal(err)
		}
		base := registry.AccessPolicy{Mode: registry.AccessSelected, Roles: map[string]string{leftIdentity.ID(): registry.WorkspaceAdmin, rightIdentity.ID(): registry.WorkspaceAdmin}}
		if _, err = left.SetAccess(ctx, filepath.Base(root), base); err != nil {
			t.Fatal(err)
		}
		bundle, exportErr := left.ExportFor(ctx, filepath.Base(root), rightIdentity.ID())
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		if _, err = right.AttachFrom(ctx, filepath.Base(root), t.TempDir(), bundle, leftIdentity.ID()); err != nil {
			t.Fatal(err)
		}
		leftPolicy := base
		leftPolicy.Roles = map[string]string{leftIdentity.ID(): registry.WorkspaceAdmin, rightIdentity.ID(): registry.WorkspaceWriter}
		rightPolicy := base
		rightPolicy.Roles = map[string]string{leftIdentity.ID(): registry.WorkspaceWriter, rightIdentity.ID(): registry.WorkspaceAdmin}
		if _, err = left.SetAccess(ctx, filepath.Base(root), leftPolicy); err != nil {
			t.Fatal(err)
		}
		if _, err = right.SetAccess(ctx, filepath.Base(root), rightPolicy); err != nil {
			t.Fatal(err)
		}
		rightBranch, exportErr := right.Export(ctx, filepath.Base(root))
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		if _, _, err = left.IntegrateFrom(ctx, filepath.Base(root), rightBranch, rightIdentity.ID()); !errors.Is(err, registry.ErrWorkspaceAccessConflict) {
			t.Fatalf("access conflict setup: %v", err)
		}
	} else {
		if _, err = left.Create(ctx, filepath.Base(root), root, state); err != nil {
			t.Fatal(err)
		}
		bundle, exportErr := left.Export(ctx, filepath.Base(root))
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		rightRoot := t.TempDir()
		if _, err = right.Attach(ctx, filepath.Base(root), rightRoot, bundle); err != nil {
			t.Fatal(err)
		}
		if _, err = left.Mutate(ctx, root, func(workspace *config.Workspace) error { workspace.Aliases["editor"] = "helix"; return nil }); err != nil {
			t.Fatal(err)
		}
		if _, err = right.Mutate(ctx, rightRoot, func(workspace *config.Workspace) error { workspace.Aliases["editor"] = "nano"; return nil }); err != nil {
			t.Fatal(err)
		}
		rightBranch, exportErr := right.Export(ctx, filepath.Base(root))
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		if _, conflicts, integrateErr := left.Integrate(ctx, filepath.Base(root), rightBranch); integrateErr != nil || len(conflicts) != 1 {
			t.Fatalf("conflict setup: conflicts=%v error=%v", conflicts, integrateErr)
		}
	}
	if err = right.Close(); err != nil {
		t.Fatal(err)
	}
	if err = left.Close(); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "personal/app")
}

func mustRegistryPath(t *testing.T) string {
	t.Helper()
	path, err := registry.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
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

func TestRunSyncCanceledDuringFinalWorkspaceExchangeReturns130(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projectRemote := testutil.InitFakeRemote(t, "app", "main")
	root := t.TempDir()
	registerSyncTestWorkspace(t, root, &config.Workspace{
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
	})
	ctx, cancel := context.WithCancel(context.Background())
	stdout := cancelOnWrite{match: "summary:", cancel: cancel}
	var stderr bytes.Buffer
	err := runSync(ctx, root, strings.NewReader(""), &stdout, &stderr)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != syncExitCanceled {
		t.Fatalf("error = %T %v, want exit %d", err, err, syncExitCanceled)
	}
	if !git.IsRepo(filepath.Join(root, "personal/app")) {
		t.Fatal("project was not synchronized before cancellation")
	}
}

type cancelOnWrite struct {
	bytes.Buffer
	match  string
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.match) {
		w.cancel()
	}
	return w.Buffer.Write(p)
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

func TestReloadSynchronizedWorkspaceWarnsWhenAliasStateCannotBeWritten(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := t.TempDir()
	registerSyncTestWorkspace(t, root, &config.Workspace{
		Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{"dev": "workspace"},
	})
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	blockedStateHome := filepath.Join(t.TempDir(), "blocked")
	if err = os.WriteFile(blockedStateHome, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blockedStateHome)
	var stderr bytes.Buffer
	workspace, err := reloadSynchronizedWorkspace(context.Background(), store, root, &stderr)
	if err != nil {
		t.Fatalf("reloadSynchronizedWorkspace: %v", err)
	}
	if workspace.Root != root || !strings.Contains(stderr.String(), "warning: could not update alias state file:") {
		t.Fatalf("workspace=%#v stderr=%q", workspace, stderr.String())
	}
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
	stdout.Reset()
	stderr.Reset()
	results = []peernetwork.SyncResult{{Device: "asahi\x1b[2J\u009b0m", Status: "unavailable"}}
	if err = writeTopLevelWorkspaceSync(&stdout, &stderr, results, nil); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stdout.String()+stderr.String(), "\x1b\u009b") || !strings.Contains(stdout.String(), `asahi\x1B[2J\x9B0m`) {
		t.Fatalf("unsafe workspace sync output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
