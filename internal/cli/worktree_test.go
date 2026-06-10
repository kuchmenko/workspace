package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

func TestValidateBranchName_AcceptsValidNames(t *testing.T) {
	cases := []string{
		"feat/auth-refactor",
		"main",
		"fix/prod-1234",
		"chore/cleanup",
		"a/b/c",
		"wt/linux/legacy-foo", // legacy form must still validate
	}
	for _, name := range cases {
		if err := validateBranchName(name); err != nil {
			t.Errorf("validateBranchName(%q) unexpectedly rejected: %v", name, err)
		}
	}
}

func TestValidateBranchName_RejectsInvalidNames(t *testing.T) {
	cases := []string{
		"feat/with spaces",
		"feat/double..dots",
		"feat~tilde",
		"-leadingdash",
		"trailing/.lock",
	}
	for _, name := range cases {
		if err := validateBranchName(name); err == nil {
			t.Errorf("validateBranchName(%q): expected error, got nil", name)
		}
	}
}

// setupTestWorkspace builds a workspace directory with one project that
// has a fake remote, a bare repo cloned from it, and a main worktree on
// the default branch. Returns the workspace root.
//
// It also pre-populates a machine config (so ensureMachineName doesn't
// prompt) and assigns the package-level ws / wsRoot globals so cobra
// commands can run against the fixture.
func setupTestWorkspace(t *testing.T, machine, projName, defaultBranch string) string {
	t.Helper()

	// Isolate state directories.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("HOME", t.TempDir())

	// Pre-populate machine config so no interactive prompt fires.
	cfgDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ws")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte(`machine_name = "`+machine+`"`+"\n"), 0o644); err != nil {
		t.Fatalf("write machine config: %v", err)
	}

	root := t.TempDir()
	remote := testutil.InitFakeRemote(t, projName, defaultBranch)

	// Clone the remote into the bare+worktree layout.
	mainPath := filepath.Join(root, "personal", projName)
	barePath := layout.BarePath(mainPath)
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	testutil.RunGit(t, t.TempDir(), "clone", "--bare", remote, barePath)
	// remote.origin.fetch refspec so subsequent fetches populate refs/remotes/origin/*
	testutil.RunGit(t, barePath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	testutil.RunGit(t, barePath, "fetch", "--all", "--prune")
	testutil.RunGit(t, barePath, "worktree", "add", mainPath, defaultBranch)

	// Write a workspace.toml with the project registered.
	wsCfg := &config.Workspace{
		Meta:    config.Meta{Version: 1, Root: root},
		Daemon:  config.Daemon{PollInterval: "5m", StaleThreshold: "30d", AutoSync: true, WatchDirs: true},
		Groups:  map[string]config.Group{},
		Aliases: map[string]string{},
		Projects: map[string]config.Project{
			projName: {
				Remote:        remote,
				Path:          filepath.Join("personal", projName),
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: defaultBranch,
			},
		},
	}
	if err := config.Save(root, wsCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Wire package-level globals so RunE can resolve the project.
	wsRoot = root
	ws = loaded
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})

	return root
}

func TestWorktreeAdd_HappyPath_NewBranchFromMain(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")

	cmd := newWorktreeAddCmd()
	cmd.SetArgs([]string{"myapp", "feat/foo"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Worktree directory should exist.
	wantDir := filepath.Join(root, "personal", "myapp-wt-linux-feat-foo")
	if _, err := os.Stat(wantDir); err != nil {
		t.Errorf("worktree dir missing: %v", err)
	}

	// workspace.toml should have a [[branches]] entry for feat/foo.
	reloaded, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	proj := reloaded.Projects["myapp"]
	meta := proj.LookupBranch("feat/foo")
	if meta == nil {
		t.Fatalf("feat/foo missing from [[branches]]")
	}
	if len(meta.Machines) != 1 || meta.Machines[0] != "linux" {
		t.Errorf("machines: want [linux], got %v", meta.Machines)
	}
	if meta.CreatedBy != "linux" {
		t.Errorf("CreatedBy: want linux, got %q", meta.CreatedBy)
	}
	if meta.LastActiveMachine != "linux" {
		t.Errorf("LastActiveMachine: want linux, got %q", meta.LastActiveMachine)
	}
}

func TestWorktreeAdd_AttachesToExistingLocalBranch(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")

	// Pre-create a local-only branch in the bare (no origin presence).
	barePath := layout.BarePath(filepath.Join(root, "personal", "myapp"))
	testutil.RunGit(t, barePath, "branch", "wt/linux/legacy-foo", "main")

	cmd := newWorktreeAddCmd()
	cmd.SetArgs([]string{"myapp", "wt/linux/legacy-foo"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wantDir := filepath.Join(root, "personal", "myapp-wt-linux-wt-linux-legacy-foo")
	if _, err := os.Stat(wantDir); err != nil {
		t.Errorf("re-registration worktree dir missing: %v", err)
	}
	reloaded, _ := config.Load(root)
	proj := reloaded.Projects["myapp"]
	if proj.LookupBranch("wt/linux/legacy-foo") == nil {
		t.Errorf("legacy branch was not registered in [[branches]]")
	}
}

// TestWorktreeAdd_PreservesLocalCommitsWhenBranchAlsoOnOrigin guards
// against the regression Codex flagged on the first revision of this
// PR: a force-fetch into refs/heads/<branch> was silently rewinding
// local branches that had unpushed commits. The fixed code uses the
// standard remote-tracking refspec instead.
func TestWorktreeAdd_PreservesLocalCommitsWhenBranchAlsoOnOrigin(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")
	barePath := layout.BarePath(filepath.Join(root, "personal", "myapp"))

	// Create a branch locally on top of main and add a unique commit on it
	// that is not on origin. We do this by checking the branch out into a
	// scratch worktree, committing, then removing the worktree (the branch
	// remains in the bare).
	scratch := filepath.Join(t.TempDir(), "scratch")
	testutil.RunGit(t, barePath, "worktree", "add", "-b", "feat/local-only", scratch, "main")
	if err := os.WriteFile(filepath.Join(scratch, "local.txt"), []byte("unpushed\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	testutil.RunGit(t, scratch, "add", "local.txt")
	testutil.RunGit(t, scratch, "commit", "-m", "unpushed commit on local branch")
	localTip := testutil.RunGit(t, scratch, "rev-parse", "feat/local-only")
	testutil.RunGit(t, barePath, "worktree", "remove", scratch)

	// Now publish the SAME branch name to origin from a completely
	// different commit (origin/main, no extra commit), to manufacture
	// the divergence the codex bug exploited.
	originPath := testutil.RunGit(t, barePath, "config", "remote.origin.url")
	pushClone := filepath.Join(t.TempDir(), "push-clone")
	testutil.RunGit(t, t.TempDir(), "clone", originPath, pushClone)
	testutil.RunGit(t, pushClone, "checkout", "-b", "feat/local-only", "origin/main")
	testutil.RunGit(t, pushClone, "push", "origin", "feat/local-only")

	// Run ws worktree add. Old (buggy) code would force-fetch
	// refs/heads/feat/local-only := origin/feat/local-only and lose
	// the local commit. New code only updates refs/remotes/origin/*
	// and attaches to the local-existing branch.
	cmd := newWorktreeAddCmd()
	cmd.SetArgs([]string{"myapp", "feat/local-only"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Tip of refs/heads/feat/local-only must still be the unpushed commit.
	gotTip := testutil.RunGit(t, barePath, "rev-parse", "feat/local-only")
	if gotTip != localTip {
		t.Errorf("local branch was rewound by the fetch\n  before: %s\n  after:  %s", localTip, gotTip)
	}
}

// TestWorktreeAdd_ReRegistersExistingCheckout guards against the P2
// Codex flagged: when the branch is already checked out in some other
// worktree (legacy wt/<machine>/* dir, or a previous add whose
// saveWorkspace step failed), `git worktree add` would refuse without
// --force. The fix is a re-registration short-circuit that updates
// metadata without trying to create a duplicate checkout.
func TestWorktreeAdd_ReRegistersExistingCheckout(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")
	barePath := layout.BarePath(filepath.Join(root, "personal", "myapp"))

	// Pre-create a worktree at a NON-canonical path (legacy wt/* style).
	legacyPath := filepath.Join(root, "personal", "myapp-wt-linux-leg-foo")
	testutil.RunGit(t, barePath, "worktree", "add", "-b", "feat/leg-foo", legacyPath, "main")

	cmd := newWorktreeAddCmd()
	cmd.SetArgs([]string{"myapp", "feat/leg-foo"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-registration should succeed, got: %v", err)
	}

	// No new worktree dir should appear at the canonical path.
	canonical := filepath.Join(root, "personal", "myapp-wt-linux-feat-leg-foo")
	if _, err := os.Stat(canonical); err == nil {
		t.Errorf("re-registration should not create a duplicate worktree at %s", canonical)
	}
	// The existing worktree must still be there.
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("existing worktree was removed: %v", err)
	}
	// Metadata must be repaired.
	reloaded, _ := config.Load(root)
	rp := reloaded.Projects["myapp"]
	meta := rp.LookupBranch("feat/leg-foo")
	if meta == nil {
		t.Fatal("re-registration did not write [[branches]] entry")
	}
	if !contains(meta.Machines, "linux") {
		t.Errorf("machines did not include linux: %v", meta.Machines)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestWorktreeAdd_RejectsInvalidName(t *testing.T) {
	setupTestWorkspace(t, "linux", "myapp", "main")
	cmd := newWorktreeAddCmd()
	cmd.SetArgs([]string{"myapp", "feat with spaces"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid branch name") {
		t.Errorf("expected invalid-branch-name error, got: %v", err)
	}
}

// TestWorktreeRm_RefusesMainWorktreeByBranch guards against the P1
// Codex flagged after the locateWorktreeForBranch refactor: when the
// user passes the project's default branch (or any branch that happens
// to be checked out at proj.path), the lookup returns the main worktree
// and the rm path no longer carried the mainPath guard. Without this
// check, `ws worktree rm myapp main` deletes the primary checkout.
func TestWorktreeRm_RefusesMainWorktreeByBranch(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")

	cmd := newWorktreeRmCmd()
	cmd.SetArgs([]string{"myapp", "main"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("ws worktree rm myapp main should fail; the main worktree must not be deletable by branch")
	}
	if !strings.Contains(err.Error(), "refusing to remove main worktree") {
		t.Errorf("error should explain the refusal, got: %v", err)
	}
	// Main worktree must still exist on disk.
	mainPath := filepath.Join(root, "personal", "myapp")
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("main worktree was removed despite the guard: %v", err)
	}
}

func TestWorktreePush_RefusesUnknownBranch(t *testing.T) {
	setupTestWorkspace(t, "linux", "myapp", "main")

	cmd := newWorktreePushCmd()
	cmd.SetArgs([]string{"myapp", "feat/never-added"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no [[branches]] entry") {
		t.Errorf("expected unknown-branch error, got: %v", err)
	}
}

func TestWorktreeRm_ReleasesMachine(t *testing.T) {
	root := setupTestWorkspace(t, "linux", "myapp", "main")

	// Add first.
	addCmd := newWorktreeAddCmd()
	addCmd.SetArgs([]string{"myapp", "feat/temp"})
	addCmd.SetOut(&bytes.Buffer{})
	addCmd.SetErr(&bytes.Buffer{})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Reload globals so the rm cmd sees the persisted state.
	reloaded, _ := config.Load(root)
	ws = reloaded

	// Then rm.
	rmCmd := newWorktreeRmCmd()
	rmCmd.SetArgs([]string{"myapp", "feat/temp"})
	rmCmd.SetOut(&bytes.Buffer{})
	rmCmd.SetErr(&bytes.Buffer{})
	if err := rmCmd.Execute(); err != nil {
		t.Fatalf("rm: %v", err)
	}

	// Empty-machines GC: entry must be gone after Save.
	final, _ := config.Load(root)
	finalProj := final.Projects["myapp"]
	if meta := finalProj.LookupBranch("feat/temp"); meta != nil {
		t.Errorf("feat/temp should be GC'd after the only machine released, got %+v", meta)
	}
}
