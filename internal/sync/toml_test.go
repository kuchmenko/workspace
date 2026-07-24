package sync

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

func TestSyncTOMLPushesEveryCommit(t *testing.T) {
	wsRoot, bareDir := setupSyncTOMLRepo(t)
	r := NewRunner(wsRoot, log.New(io.Discard, "", 0))

	appendFile(t, filepath.Join(wsRoot, "workspace.toml"), "# edit A\n")
	if _, err := r.syncTOML(); err != nil {
		t.Fatalf("syncTOML A: %v", err)
	}
	headA := testutil.RunGit(t, wsRoot, "rev-parse", "HEAD")
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != headA {
		t.Fatalf("expected immediate push to %s, remote at %s", headA, got)
	}

	appendFile(t, filepath.Join(wsRoot, "workspace.toml"), "# edit B\n")
	if _, err := r.syncTOML(); err != nil {
		t.Fatalf("syncTOML B: %v", err)
	}
	headB := testutil.RunGit(t, wsRoot, "rev-parse", "HEAD")
	if headB == headA {
		t.Fatalf("expected a fresh commit; HEAD unchanged at %s", headA)
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != headB {
		t.Fatalf("expected immediate push to %s, remote at %s", headB, got)
	}
}

func TestSyncTOMLRefusesToPushInvalidWorkspaceTOML(t *testing.T) {
	wsRoot, bareDir := setupSyncTOMLRepo(t)
	r := NewRunner(wsRoot, log.New(io.Discard, "", 0))

	remoteHead := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main")
	appendFile(t, filepath.Join(wsRoot, "workspace.toml"), `
[[projects.app.branches]]
  name = "main"
  machines = ["linux"]
  name = "feat/stp"
  machines = ["archlinux"]
`)

	if _, err := r.syncTOML(); err == nil {
		t.Fatal("syncTOML should reject invalid workspace.toml")
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("invalid workspace.toml was pushed; remote moved from %s to %s", remoteHead, got)
	}
}

func TestRunContextPushesProjectConversionWithWorkspaceRegistry(t *testing.T) {
	root, workspaceRemote := setupSyncTOMLRepo(t)
	projectRemote := testutil.InitFakeRemote(t, "converted-project", "main")
	configureTestSSH(t, projectRemote)
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	project := workspace.Projects["app"]
	project.Remote = testHTTPSRemote
	project.Path = "blocked"
	workspace.Projects["app"] = project
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	if err := selection.SelectConversion(plan.Projects[0].OriginID); err != nil {
		t.Fatal(err)
	}

	report := NewRunner(root, log.New(io.Discard, "", 0)).RunContext(context.Background(), selection, nil)
	if len(report.Conversions) != 1 || report.Conversions[0].Status != ResultSuccess {
		t.Fatalf("conversions = %+v", report.Conversions)
	}
	remoteTOML := testutil.RunGit(t, workspaceRemote, "show", "refs/heads/main:workspace.toml")
	candidate := "git@github.com:acme/workspace-sync-test.git"
	if !strings.Contains(remoteTOML, candidate) {
		t.Fatalf("pushed workspace.toml does not contain converted remote %q:\n%s", candidate, remoteTOML)
	}
	if got, err := git.ConfiguredRemoteURL(root, "origin"); err != nil || got != workspaceRemote {
		t.Fatalf("workspace origin = %q, %v", got, err)
	}
}

func TestRunContextWorkspaceUsesOnlyFrozenOrigin(t *testing.T) {
	root, workspaceRemote := setupSyncTOMLRepo(t)
	testutil.RunGit(t, root, "remote", "add", "unplanned", filepath.Join(t.TempDir(), "missing.git"))
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Projects = map[string]config.Project{}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	appendFile(t, filepath.Join(root, "workspace.toml"), "# origin-only\n")

	report := NewRunner(root, log.New(io.Discard, "", 0)).RunContext(context.Background(), selection, nil)
	for _, result := range report.Workspace {
		if result.Status == ResultFailed {
			t.Fatalf("workspace results = %+v", report.Workspace)
		}
	}
	if got := testutil.RunGit(t, workspaceRemote, "show", "refs/heads/main:workspace.toml"); !strings.Contains(got, "# origin-only") {
		t.Fatalf("workspace edit was not pushed:\n%s", got)
	}
}

func TestRunContextWorkspaceRejectsChangedOriginBeforeNetwork(t *testing.T) {
	root, _ := setupSyncTOMLRepo(t)
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Projects = map[string]config.Project{}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	changedOrigin := filepath.Join(t.TempDir(), "missing.git")
	testutil.RunGit(t, root, "remote", "set-url", "origin", changedOrigin)

	report := NewRunner(root, log.New(io.Discard, "", 0)).RunContext(context.Background(), selection, nil)
	if len(report.Workspace) != 1 || report.Workspace[0].Status != ResultFailed || !strings.Contains(report.Workspace[0].Diagnostic, "workspace origin changed after preflight") {
		t.Fatalf("workspace results = %+v", report.Workspace)
	}
}

func TestRunContextRejectsChangedWorkspaceBranchBeforeMutation(t *testing.T) {
	root, workspaceRemote := setupSyncTOMLRepo(t)
	workspace, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Projects = map[string]config.Project{}
	if err := config.Save(root, workspace); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, root, "add", "workspace.toml")
	testutil.RunGit(t, root, "commit", "-m", "remove projects")
	testutil.RunGit(t, root, "push", "origin", "main")
	plan := BuildPlan(root, workspace)
	selection := NewSelection(plan, Probe(context.Background(), plan, nil))
	remoteHead := git.RevParse(workspaceRemote, "refs/heads/main")
	testutil.RunGit(t, root, "checkout", "-b", "other")
	appendFile(t, filepath.Join(root, "workspace.toml"), "# must remain uncommitted\n")
	localHead := git.RevParse(root, "HEAD")

	report := NewRunner(root, log.New(io.Discard, "", 0)).RunContext(context.Background(), selection, nil)
	if len(report.Workspace) != 1 || report.Workspace[0].Status != ResultFailed || !strings.Contains(report.Workspace[0].Diagnostic, "workspace branch changed after preflight") {
		t.Fatalf("workspace results = %+v", report.Workspace)
	}
	if got := git.RevParse(root, "HEAD"); got != localHead {
		t.Fatalf("local HEAD changed from %s to %s", localHead, got)
	}
	if got := git.RevParse(workspaceRemote, "refs/heads/main"); got != remoteHead {
		t.Fatalf("remote main changed from %s to %s", remoteHead, got)
	}
}

func setupSyncTOMLRepo(t *testing.T) (string, string) {
	t.Helper()
	bareDir := testutil.InitFakeRemote(t, "ws-toml", "main")
	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "ws")
	testutil.RunGit(t, tmp, "clone", bareDir, "ws")
	testutil.RunGit(t, wsRoot, "config", "user.name", "ws-test")
	testutil.RunGit(t, wsRoot, "config", "user.email", "test@example.invalid")
	testutil.RunGit(t, wsRoot, "config", "commit.gpgsign", "false")
	testutil.RunGit(t, wsRoot, "config", "tag.gpgsign", "false")

	tomlPath := filepath.Join(wsRoot, "workspace.toml")
	if err := os.WriteFile(tomlPath, []byte(seedWorkspaceTOML()), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, wsRoot, "add", "workspace.toml")
	attrPath := filepath.Join(wsRoot, ".gitattributes")
	if err := os.WriteFile(attrPath, []byte("workspace.toml merge=union\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, wsRoot, "add", ".gitattributes")
	testutil.RunGit(t, wsRoot, "commit", "-m", "seed workspace.toml")
	testutil.RunGit(t, wsRoot, "push", "-u", "origin", "main")
	return wsRoot, bareDir
}

func seedWorkspaceTOML() string {
	return `
[meta]
version = 1
root = "/tmp/ws"

[projects.app]
remote = "git@example.com:app.git"
path = "personal/app"
status = "active"
category = "personal"
`
}

func appendFile(t *testing.T, path, value string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(value); err != nil {
		t.Fatal(err)
	}
}
