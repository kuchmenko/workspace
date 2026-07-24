package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	workspacesync "codeberg.org/kuchmenko/workspace/internal/sync"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
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
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	plan := workspacesync.BuildPlan(root, workspace)
	var stdout, stderr bytes.Buffer
	err = runSyncHeadless(context.Background(), root, plan, &stdout, &stderr)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != syncExitFailed {
		t.Fatalf("error = %v, want exit %d", err, syncExitFailed)
	}
	if _, err := os.Stat(filepath.Join(root, "personal/app")); !os.IsNotExist(err) {
		t.Fatalf("project path was mutated: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("workspace.toml changed after failed preflight")
	}
	output := stdout.String() + stderr.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("headless output contains ANSI: %q", output)
	}
	if !strings.Contains(stdout.String(), "no changes made") {
		t.Fatalf("missing no-mutation summary: %s", stdout.String())
	}
}

func TestRunSyncHeadlessExecutesAllAfterSuccessfulPreflight(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	workspaceRemote := testutil.InitFakeRemote(t, "workspace", "main")
	projectRemote := testutil.InitFakeRemote(t, "app", "main")
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	testutil.RunGit(t, parent, "clone", workspaceRemote, root)
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
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(stdout.String(), "start: workspace-sync") || !strings.Contains(stdout.String(), "start: project-sync app") {
		t.Fatalf("missing execution progress:\n%s", stdout.String())
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
