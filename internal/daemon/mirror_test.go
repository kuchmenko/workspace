package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

// setupMirrorProject builds a workspace root holding one project in
// bare+worktree layout cloned from a fake origin, plus an empty fake
// remote acting as the mirror. XDG_STATE_HOME is isolated FIRST so the
// reconciler's conflict store never touches the real one.
func setupMirrorProject(t *testing.T) (r *Reconciler, proj config.Project, barePath, originURL, mirrorURL string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	originURL = testutil.InitFakeRemote(t, "proj", "main")
	wsRoot := t.TempDir()

	mainPath := filepath.Join(wsRoot, "personal", "proj")
	barePath = layout.BarePath(mainPath)
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := git.CloneBare(originURL, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("SetFetchRefspec: %v", err)
	}
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := git.SetBranchUpstream(barePath, "main", "origin"); err != nil {
		t.Fatalf("SetBranchUpstream: %v", err)
	}
	if err := git.WorktreeAdd(barePath, mainPath, "main", ""); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	mirrorURL = filepath.Join(t.TempDir(), "mirror.git")
	testutil.RunGit(t, filepath.Dir(mirrorURL), "init", "--bare", "--initial-branch=main", mirrorURL)

	proj = config.Project{
		Remote:  originURL,
		Path:    "personal/proj",
		Status:  "active",
		Mirrors: map[string]string{"github": mirrorURL},
	}
	r = NewReconciler(wsRoot, 5*time.Minute, log.New(io.Discard, "", 0))
	return r, proj, barePath, originURL, mirrorURL
}

func mirrorConflicts(t *testing.T, r *Reconciler) []Conflict {
	t.Helper()
	all, err := r.store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	var out []Conflict
	for _, c := range all {
		if c.Kind == KindMirrorPushFailed {
			out = append(out, c)
		}
	}
	return out
}

func TestSyncProject_MirrorReceivesAndCatchesUp(t *testing.T) {
	r, proj, barePath, originURL, mirrorURL := setupMirrorProject(t)

	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	if !git.MirrorRemoteOK(barePath, "github", mirrorURL) {
		t.Error("mirror remote not installed by syncProject")
	}
	if got := testutil.RunGit(t, mirrorURL, "rev-parse", "refs/heads/main"); got != git.RevParse(barePath, "refs/remotes/origin/main") {
		t.Errorf("mirror main = %s, want origin's", got)
	}
	if cs := mirrorConflicts(t, r); len(cs) != 0 {
		t.Errorf("unexpected mirror conflicts: %+v", cs)
	}

	// Origin advances → next tick the mirror catches up.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.RunGit(t, filepath.Dir(seed), "clone", originURL, seed)
	if err := os.WriteFile(filepath.Join(seed, "n.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, seed, "add", "n.txt")
	testutil.RunGit(t, seed, "commit", "-m", "advance")
	testutil.RunGit(t, seed, "push", "origin", "main")

	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("second syncProject: %v", err)
	}
	want := git.RevParse(barePath, "refs/remotes/origin/main")
	if got := testutil.RunGit(t, mirrorURL, "rev-parse", "refs/heads/main"); got != want {
		t.Errorf("mirror main = %s, want %s after origin advanced", got, want)
	}
}

func TestSyncProject_DivergedMirrorConflictAndClear(t *testing.T) {
	r, proj, _, _, mirrorURL := setupMirrorProject(t)

	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	goodHead := testutil.RunGit(t, mirrorURL, "rev-parse", "refs/heads/main")

	// Foreign rewrite directly on the mirror → push diverges.
	foreign := filepath.Join(t.TempDir(), "foreign")
	testutil.RunGit(t, filepath.Dir(foreign), "clone", mirrorURL, foreign)
	testutil.RunGit(t, foreign, "commit", "--amend", "--allow-empty", "-m", "rewritten")
	testutil.RunGit(t, foreign, "push", "--force", "origin", "main")

	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject with diverged mirror should not error: %v", err)
	}
	cs := mirrorConflicts(t, r)
	if len(cs) != 1 {
		t.Fatalf("want 1 mirror conflict, got %d: %+v", len(cs), cs)
	}
	if cs[0].Project != "proj" || cs[0].Branch != "github" {
		t.Errorf("conflict keyed wrong: project=%s branch=%s", cs[0].Project, cs[0].Branch)
	}

	// Reset the mirror → conflict clears on next tick.
	testutil.RunGit(t, mirrorURL, "update-ref", "refs/heads/main", goodHead)
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject after reset: %v", err)
	}
	if cs := mirrorConflicts(t, r); len(cs) != 0 {
		t.Errorf("conflict not cleared after mirror reset: %+v", cs)
	}
}

func TestSyncProject_DeadMirrorDoesNotBlockMainFF(t *testing.T) {
	r, proj, barePath, originURL, _ := setupMirrorProject(t)
	proj.Mirrors = map[string]string{"github": filepath.Join(t.TempDir(), "nonexistent.git")}

	// Origin advances while the mirror is dead.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.RunGit(t, filepath.Dir(seed), "clone", originURL, seed)
	if err := os.WriteFile(filepath.Join(seed, "n.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, seed, "add", "n.txt")
	testutil.RunGit(t, seed, "commit", "-m", "advance")
	testutil.RunGit(t, seed, "push", "origin", "main")

	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("dead mirror must not fail syncProject: %v", err)
	}
	if len(mirrorConflicts(t, r)) != 1 {
		t.Error("dead mirror should record a conflict")
	}
	// Main worktree still fast-forwarded.
	mainPath := filepath.Join(r.root, proj.Path)
	want := git.RevParse(barePath, "refs/remotes/origin/main")
	if got := testutil.RunGit(t, mainPath, "rev-parse", "HEAD"); got != want {
		t.Errorf("main worktree HEAD = %s, want %s (FF must not be blocked)", got, want)
	}
}

func TestSyncProject_RemovedMirrorClearsStaleConflict(t *testing.T) {
	r, proj, _, _, _ := setupMirrorProject(t)
	proj.Mirrors = map[string]string{"github": filepath.Join(t.TempDir(), "nonexistent.git")}

	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	if len(mirrorConflicts(t, r)) != 1 {
		t.Fatal("expected a conflict for the dead mirror")
	}

	proj.Mirrors = nil
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject after mirror removal: %v", err)
	}
	if cs := mirrorConflicts(t, r); len(cs) != 0 {
		t.Errorf("stale mirror conflict not cleared: %+v", cs)
	}
}
