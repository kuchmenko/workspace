package sync

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/conflict"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

func setupMirrorProject(t *testing.T) (r *Runner, proj config.Project, barePath, originURL, mirrorURL string) {
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
	r = NewRunner(wsRoot, log.New(io.Discard, "", 0))
	return r, proj, barePath, originURL, mirrorURL
}

func mirrorConflicts(t *testing.T, r *Runner) []conflict.Conflict {
	t.Helper()
	all, err := r.store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	var out []conflict.Conflict
	for _, c := range all {
		if c.Kind == conflict.KindMirrorPushFailed {
			out = append(out, c)
		}
	}
	return out
}

func TestSyncProjectMirrorReceivesAndCatchesUp(t *testing.T) {
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

func TestSyncProjectDivergedMirrorConflictAndClear(t *testing.T) {
	r, proj, _, _, mirrorURL := setupMirrorProject(t)
	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	goodHead := testutil.RunGit(t, mirrorURL, "rev-parse", "refs/heads/main")
	foreign := filepath.Join(t.TempDir(), "foreign")
	testutil.RunGit(t, filepath.Dir(foreign), "clone", mirrorURL, foreign)
	testutil.RunGit(t, foreign, "commit", "--amend", "--allow-empty", "-m", "rewritten")
	testutil.RunGit(t, foreign, "push", "--force", "origin", "main")
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject with diverged mirror should not error: %v", err)
	}
	cs := mirrorConflicts(t, r)
	if len(cs) != 1 || cs[0].Project != "proj" || cs[0].Branch != "github" {
		t.Fatalf("unexpected mirror conflicts: %+v", cs)
	}
	testutil.RunGit(t, mirrorURL, "update-ref", "refs/heads/main", goodHead)
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject after reset: %v", err)
	}
	if cs := mirrorConflicts(t, r); len(cs) != 0 {
		t.Errorf("conflict not cleared after mirror reset: %+v", cs)
	}
}

func TestSyncProjectDeadMirrorDoesNotBlockMainFF(t *testing.T) {
	r, proj, barePath, originURL, _ := setupMirrorProject(t)
	proj.Mirrors = map[string]string{"github": filepath.Join(t.TempDir(), "nonexistent.git")}
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
	mainPath := filepath.Join(r.root, proj.Path)
	want := git.RevParse(barePath, "refs/remotes/origin/main")
	if got := testutil.RunGit(t, mainPath, "rev-parse", "HEAD"); got != want {
		t.Errorf("main worktree HEAD = %s, want %s", got, want)
	}
}

func TestSyncProjectRemovedMirrorClearsStaleConflict(t *testing.T) {
	r, proj, _, _, _ := setupMirrorProject(t)
	proj.Mirrors = map[string]string{"github": filepath.Join(t.TempDir(), "nonexistent.git")}
	touched := false
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject: %v", err)
	}
	proj.Mirrors = nil
	if err := r.syncProject("proj", &proj, "machine-a", &touched); err != nil {
		t.Fatalf("syncProject after mirror removal: %v", err)
	}
	if cs := mirrorConflicts(t, r); len(cs) != 0 {
		t.Errorf("stale mirror conflict not cleared: %+v", cs)
	}
}

func TestMirrorConflictDetailsRedactCredentialURL(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := NewRunner(t.TempDir(), log.New(io.Discard, "", 0))
	raw := "https://user:secret@example.com/owner/project.git"
	r.recordMirrorConflict("project", "backup", raw, fmt.Errorf("push to %s failed", raw))
	conflicts := mirrorConflicts(t, r)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	details := string(conflicts[0].Details)
	if strings.Contains(details, raw) || strings.Contains(details, "secret") || strings.Contains(details, "user:") {
		t.Fatalf("credential URL persisted in conflict: %s", details)
	}
	if !strings.Contains(details, "REDACTED") {
		t.Fatalf("conflict details = %s, want redaction marker", details)
	}
}
