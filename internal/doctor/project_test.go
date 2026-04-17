package doctor

import (
	"os"
	"path/filepath"
	"testing"

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

	if err := git.CloneBare(remote, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}
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

	rep := r.Run()
	for _, f := range rep.Findings {
		if f.Scope == "system" && f.Check == "daemon" {
			// Daemon not running in tests — expected and unrelated.
			continue
		}
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

func TestCheckDefaultBranch_DetectAndPersist(t *testing.T) {
	wsRoot := t.TempDir()

	// Seed workspace.toml so config.Save has a real file to rewrite.
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	proj.DefaultBranch = "" // simulate drift/missing field

	ws := &config.Workspace{
		Meta:     config.Meta{Version: 1, Root: wsRoot},
		Projects: map[string]config.Project{"demo": proj},
	}
	if err := config.Save(wsRoot, ws); err != nil {
		t.Fatalf("config.Save seed: %v", err)
	}

	r := &Runner{WsRoot: wsRoot, WS: ws, SkipRemote: true}

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

	reloaded, err := config.Load(wsRoot)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
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
	mainWT := filepath.Join(wsRoot, proj.Path)
	lockFile := filepath.Join(mainWT, ".git")
	// .git in a worktree is a file pointing to gitdir; resolve real gitdir.
	gitDir := git.RevParse(mainWT, "--git-dir")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(mainWT, gitDir)
	}
	lockFile = filepath.Join(gitDir, "index.lock")
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
