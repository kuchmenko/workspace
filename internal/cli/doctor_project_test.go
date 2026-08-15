package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// makeProjectBare builds a realistic bare+worktree layout at wsRoot/name
// backed by a fake remote. Returns (projRel, barePath) so tests can pass
// the relative path to the Runner and the absolute bare path to the
// low-level checks. The returned bare has the standard fetch refspec
// installed and a populated origin/main, mirroring what
// clone.CloneIntoLayout produces in production.
func makeProjectBare(t *testing.T, wsRoot, name, defaultBranch string) (config.Project, string) {
	t.Helper()
	remote := testutil.InitFakeRemote(t, name, defaultBranch)

	projRel := filepath.Join("personal", name)
	mainPath := filepath.Join(wsRoot, projRel)
	barePath := mainPath + ".bare"
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}

	testutil.CloneBare(t, remote, barePath)
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("SetFetchRefspec: %v", err)
	}
	// Fetch so refs/remotes/origin/* get populated (needed for
	// default-branch detection and branch-upstream resolution).
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := git.SetRemoteHead(barePath, defaultBranch); err != nil {
		t.Fatalf("SetRemoteHead: %v", err)
	}
	// Add the main worktree so WorktreeList has something beyond the bare.
	if err := git.WorktreeAdd(barePath, mainPath, defaultBranch, ""); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	// Wire upstream so branch-upstream check passes on the happy path.
	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		t.Fatalf("SetBranchUpstream: %v", err)
	}

	return config.Project{
		Remote:        remote,
		Path:          projRel,
		Status:        config.StatusActive,
		Category:      config.CategoryPersonal,
		DefaultBranch: defaultBranch,
	}, barePath
}

func newRunnerFor(t *testing.T, wsRoot string, projects map[string]config.Project) *Runner {
	t.Helper()
	ws := &config.Workspace{Projects: projects}
	return &Runner{
		WsRoot:     wsRoot,
		WS:         ws,
		SkipRemote: true, // network hit covered separately
	}
}

// The happy path must emit no Warn / Error findings for any project check.
// This is the regression test for the whole catalog: adding a check that
// flags a fresh clone as broken will blow up here.
func TestProjectChecks_HappyPath(t *testing.T) {
	wsRoot := t.TempDir()
	proj, _ := makeProjectBare(t, wsRoot, "demo", "main")
	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})

	rep := r.Run(context.Background())
	for _, f := range rep.Findings {
		if f.Severity >= Warn {
			t.Errorf("unexpected %s: %s/%s: %s", f.Severity, f.Scope, f.Check, f.Message)
		}
	}
}

func TestCheckFetchRefspec_MissingAndFix(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	// Break the invariant: unset remote.origin.fetch so the check fires.
	testutil.RunGit(t, barePath, "config", "--unset", "remote.origin.fetch")

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})
	f := r.checkFetchRefspec("demo", barePath)
	if f.Severity != Error {
		t.Fatalf("Severity=%s want Error", f.Severity)
	}
	if f.Fix == nil {
		t.Fatal("fetch-refspec missing must offer an auto-fix")
	}
	if err := f.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	after := r.checkFetchRefspec("demo", barePath)
	if after.Severity != OK {
		t.Fatalf("after fix: Severity=%s want OK", after.Severity)
	}
}

func TestCheckRemoteURL_Mismatch(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})

	// Sanity: happy path ok.
	if got := r.checkRemoteURL("demo", proj, barePath); got.Severity != OK {
		t.Fatalf("happy path: Severity=%s want OK (%s)", got.Severity, got.Message)
	}

	// Drift: bare's origin points somewhere else.
	testutil.RunGit(t, barePath, "remote", "set-url", "origin", "git@example.com:other/repo.git")
	got := r.checkRemoteURL("demo", proj, barePath)
	if got.Severity != Error {
		t.Fatalf("drift: Severity=%s want Error", got.Severity)
	}
	if got.Fix == nil {
		t.Fatal("drift must offer auto-fix")
	}
	if err := got.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	after, err := git.RemoteURL(barePath)
	if err != nil {
		t.Fatalf("RemoteURL: %v", err)
	}
	if after != proj.Remote {
		t.Fatalf("after fix: origin=%q want %q", after, proj.Remote)
	}
}

func TestDoctorFindingsRedactRemoteCredentials(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	actual := "https://actual-user:actual-secret@example.com/demo.git"
	declared := "https://declared-user:declared-secret@example.com/demo.git"
	mirror := "https://mirror-user:mirror-secret@example.com/demo.git"
	testutil.RunGit(t, barePath, "remote", "set-url", "origin", actual)
	proj.Remote = declared
	proj.Mirrors = map[string]string{"backup": mirror}
	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})
	findings := []Finding{r.checkRemoteURL("demo", proj, barePath)}
	findings = append(findings, r.checkMirrorRemotes("demo", proj, barePath)...)
	report := &Report{Findings: findings}

	var text strings.Builder
	WriteText(&text, report)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{text.String(), string(encoded)} {
		for _, secret := range []string{"actual-user", "actual-secret", "declared-user", "declared-secret", "mirror-user", "mirror-secret"} {
			if strings.Contains(output, secret) {
				t.Fatalf("doctor output leaked %q: %s", secret, output)
			}
		}
	}
}

func TestCheckRemoteReachRedactsDiagnostic(t *testing.T) {
	barePath := t.TempDir()
	realGit := gitExecutable(t)
	credentialURL := "https://diagnostic-user:diagnostic-secret@example.com/demo.git"
	testutil.RunGit(t, barePath, "init", "--bare")
	testutil.RunGit(t, barePath, "remote", "add", "origin", credentialURL)
	installGitWrapper(t, realGit, fmt.Sprintf("echo 'fatal: unable to access %s' >&2\nexit 1", credentialURL))

	finding := (&Runner{}).checkRemoteReach(context.Background(), "demo", barePath)
	if strings.Contains(finding.Message, "diagnostic-user") || strings.Contains(finding.Message, "diagnostic-secret") {
		t.Fatalf("diagnostic leaked credentials: %s", finding.Message)
	}
	encoded, err := json.Marshal(&Report{Findings: []Finding{finding}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "diagnostic-secret") {
		t.Fatalf("JSON leaked credentials: %s", encoded)
	}
}

func TestRunnerCancellationStopsRemoteAndSkipsRemainingProjects(t *testing.T) {
	wsRoot := t.TempDir()
	alpha, _ := makeProjectBare(t, wsRoot, "alpha", "main")
	beta, _ := makeProjectBare(t, wsRoot, "beta", "main")
	realGit := gitExecutable(t)
	marker := filepath.Join(t.TempDir(), "ls-remote")
	installGitWrapper(t, realGit, fmt.Sprintf("echo x >> %q\nexec sleep 30", marker))
	r := &Runner{WsRoot: wsRoot, WS: &config.Workspace{Projects: map[string]config.Project{"alpha": alpha, "beta": beta}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *Report, 1)
	go func() { done <- r.Run(ctx) }()
	waitForFile(t, marker)
	cancel()

	select {
	case report := <-done:
		for _, finding := range report.Findings {
			if finding.Scope == "beta" {
				t.Fatal("beta checks started after cancellation")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active ls-remote did not stop after cancellation")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("ls-remote calls=%d want 1", got)
	}
}

func gitExecutable(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	realGit, err := filepath.EvalSymlinks(gitPath)
	if err != nil {
		t.Fatalf("resolve git: %v", err)
	}
	return realGit
}

func installGitWrapper(t *testing.T, realGit, lsRemoteBody string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *ls-remote*)\n" + lsRemoteBody + "\n    ;;\n  *) exec " + realGit + " \"$@\" ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for ls-remote")
}

func TestCheckDefaultBranch_DetectAndPersist(t *testing.T) {
	wsRoot := t.TempDir()

	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	proj.DefaultBranch = "" // simulate drift/missing field

	ws := &config.Workspace{
		Meta:     config.Meta{Version: 1, Root: wsRoot},
		Projects: map[string]config.Project{"demo": proj},
	}
	setTestRegistryWorkspace(t, wsRoot, ws)

	r := &Runner{WsRoot: wsRoot, WS: registryState.State, SkipRemote: true}

	f := r.checkDefaultBranch("demo", proj, barePath)
	if f.Severity != Warn {
		t.Fatalf("Severity=%s want Warn (%s)", f.Severity, f.Message)
	}
	if f.Fix == nil {
		t.Fatal("detected default branch must offer persist auto-fix")
	}
	if err := f.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	reloadedState, err := registryStore.LoadByRoot(context.Background(), wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := reloadedState.State
	if got := reloaded.Projects["demo"].DefaultBranch; got != "main" {
		t.Fatalf("persisted default_branch=%q want main", got)
	}
}

func TestCheckBranchUpstream_MissingAndFix(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")

	// Break upstream thoroughly: unset config AND delete the tracking
	// ref. Setting config alone is not enough to make HasUpstream pass
	// — git's @{upstream} resolution needs refs/remotes/origin/<X> to
	// actually exist. This is the exact state of a bare that was cloned
	// pre-PR#16 and then had its refspec fixed but never re-fetched
	// (observed repeatedly in the wild when the fix was split incorrectly).
	testutil.RunGit(t, barePath, "config", "--unset", "branch.main.remote")
	testutil.RunGit(t, barePath, "config", "--unset", "branch.main.merge")
	testutil.RunGit(t, barePath, "update-ref", "-d", "refs/remotes/origin/main")

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})
	r.SkipRemote = false // fix must fetch to populate tracking ref

	f := r.checkBranchUpstream("demo", proj, barePath)
	if f.Severity != Warn {
		t.Fatalf("Severity=%s want Warn", f.Severity)
	}
	if f.Fix == nil {
		t.Fatal("missing upstream must offer auto-fix")
	}
	if err := f.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !git.HasUpstream(barePath, "main") {
		t.Fatal("HasUpstream=false after fix — tracking ref not repopulated")
	}
}

// SkipRemote must write config but refuse to touch the network, even
// if that means HasUpstream still reports false afterwards. The user
// opted into offline operation; the next online fetch will complete
// the picture.
func TestCheckBranchUpstream_SkipRemote(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	testutil.RunGit(t, barePath, "config", "--unset", "branch.main.remote")
	testutil.RunGit(t, barePath, "config", "--unset", "branch.main.merge")
	testutil.RunGit(t, barePath, "update-ref", "-d", "refs/remotes/origin/main")

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})
	r.SkipRemote = true

	f := r.checkBranchUpstream("demo", proj, barePath)
	if err := f.Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if remote := testutil.RunGit(t, barePath, "config", "--get", "branch.main.remote"); remote != "origin" {
		t.Fatalf("branch.main.remote=%q want origin", remote)
	}
	if err := testutil.RunGitTry(t, barePath, "show-ref", "--verify", "--quiet", "refs/remotes/origin/main"); err == nil {
		t.Fatal("refs/remotes/origin/main should NOT be populated when SkipRemote is set")
	}
}

func TestCheckIndexLock(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})

	// No locks initially.
	clean := r.checkIndexLock("demo", barePath)
	if len(clean) != 1 || clean[0].Severity != OK {
		t.Fatalf("clean state: %+v", clean)
	}

	// Plant an index.lock in the main worktree and re-run.
	// .git in a worktree is a file pointing to gitdir; resolve real gitdir.
	mainWT := filepath.Join(wsRoot, proj.Path)
	gitDir := git.RevParse(mainWT, "--git-dir")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(mainWT, gitDir)
	}
	lockFile := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lockFile, []byte{}, 0o644); err != nil {
		t.Fatalf("plant lock: %v", err)
	}

	got := r.checkIndexLock("demo", barePath)
	if len(got) != 1 {
		t.Fatalf("findings=%d want 1", len(got))
	}
	if got[0].Severity != Warn {
		t.Fatalf("Severity=%s want Warn", got[0].Severity)
	}
	if got[0].Fix != nil {
		t.Fatal("index-lock must NOT offer an auto-fix (risky)")
	}
}

func TestPathExists(t *testing.T) {
	dir := t.TempDir()
	if !pathExists(dir) {
		t.Error("tempdir not found")
	}
	if pathExists(filepath.Join(dir, "does-not-exist")) {
		t.Error("missing path reported as existing")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 20); got != "short" {
		t.Errorf("short: got %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("long: got %q", got)
	}
}
