package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

// mirrorFixture builds a bare clone of a fake origin (with fetch refspec
// applied and refs fetched) plus an empty fake remote to act as the mirror.
func mirrorFixture(t *testing.T) (barePath, mirrorURL string) {
	t.Helper()
	origin := testutil.InitFakeRemote(t, "proj", "main")
	barePath = filepath.Join(t.TempDir(), "proj.bare")
	if err := git.CloneBare(origin, barePath); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}
	if err := git.SetFetchRefspec(barePath); err != nil {
		t.Fatalf("SetFetchRefspec: %v", err)
	}
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	mirrorURL = filepath.Join(t.TempDir(), "mirror.git")
	testutil.RunGit(t, filepath.Dir(mirrorURL), "init", "--bare", "--initial-branch=main", mirrorURL)
	return barePath, mirrorURL
}

func TestEnsureMirrorRemote(t *testing.T) {
	barePath, mirrorURL := mirrorFixture(t)

	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("EnsureMirrorRemote: %v", err)
	}
	if !git.MirrorRemoteOK(barePath, "github", mirrorURL) {
		t.Error("MirrorRemoteOK false after EnsureMirrorRemote")
	}
	got := testutil.RunGit(t, barePath, "config", "--get", "remote.github.skipFetchAll")
	if got != "true" {
		t.Errorf("remote.github.skipFetchAll = %q, want true", got)
	}

	// Idempotent.
	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("second EnsureMirrorRemote: %v", err)
	}

	// Repairs URL drift.
	testutil.RunGit(t, barePath, "remote", "set-url", "github", "/nonexistent")
	if git.MirrorRemoteOK(barePath, "github", mirrorURL) {
		t.Error("MirrorRemoteOK should be false after URL drift")
	}
	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("EnsureMirrorRemote repair: %v", err)
	}
	if !git.MirrorRemoteOK(barePath, "github", mirrorURL) {
		t.Error("MirrorRemoteOK false after repair")
	}

	// "origin" and empty names are reserved.
	if err := git.EnsureMirrorRemote(barePath, "origin", mirrorURL); err == nil {
		t.Error("EnsureMirrorRemote(origin) should fail")
	}
	if err := git.EnsureMirrorRemote(barePath, "", mirrorURL); err == nil {
		t.Error("EnsureMirrorRemote(\"\") should fail")
	}
}

func TestPushMirror(t *testing.T) {
	barePath, mirrorURL := mirrorFixture(t)

	// Add a feature branch plus annotated and lightweight tags on origin,
	// then re-fetch the bare so it has everything to mirror.
	origin, err := git.RemoteURL(barePath)
	if err != nil {
		t.Fatalf("RemoteURL: %v", err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.RunGit(t, filepath.Dir(seed), "clone", origin, seed)
	testutil.RunGit(t, seed, "checkout", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(seed, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, seed, "add", "x.txt")
	testutil.RunGit(t, seed, "commit", "-m", "x")
	testutil.RunGit(t, seed, "tag", "-a", "v1.0.0", "-m", "release")
	testutil.RunGit(t, seed, "tag", "light")
	testutil.RunGit(t, seed, "push", "origin", "feature/x", "v1.0.0", "light")
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("EnsureMirrorRemote: %v", err)
	}
	if err := git.PushMirror(barePath, "github"); err != nil {
		t.Fatalf("PushMirror: %v", err)
	}

	for _, ref := range []string{"refs/heads/main", "refs/heads/feature/x", "refs/tags/v1.0.0", "refs/tags/light"} {
		if testutil.RunGit(t, mirrorURL, "rev-parse", "--verify", ref) == "" {
			t.Errorf("mirror missing %s after PushMirror", ref)
		}
	}
	// origin/HEAD symref must not become a literal "HEAD" branch.
	if git.RevParse(mirrorURL, "refs/heads/HEAD") != "" {
		t.Error("mirror has a refs/heads/HEAD branch — negative refspec did not apply")
	}

	// Mirror catches up after origin advances.
	if err := os.WriteFile(filepath.Join(seed, "y.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, seed, "add", "y.txt")
	testutil.RunGit(t, seed, "commit", "-m", "y")
	testutil.RunGit(t, seed, "push", "origin", "feature/x")
	if err := git.Fetch(barePath); err != nil {
		t.Fatalf("re-Fetch: %v", err)
	}
	if err := git.PushMirror(barePath, "github"); err != nil {
		t.Fatalf("second PushMirror: %v", err)
	}
	want := git.RevParse(barePath, "refs/remotes/origin/feature/x")
	got := testutil.RunGit(t, mirrorURL, "rev-parse", "refs/heads/feature/x")
	if got != want {
		t.Errorf("mirror feature/x = %s, want %s", got, want)
	}
}

func TestPushMirror_NonFastForwardFails(t *testing.T) {
	barePath, mirrorURL := mirrorFixture(t)
	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("EnsureMirrorRemote: %v", err)
	}
	if err := git.PushMirror(barePath, "github"); err != nil {
		t.Fatalf("initial PushMirror: %v", err)
	}

	// Foreign commit lands directly on the mirror → next push is non-ff.
	foreign := filepath.Join(t.TempDir(), "foreign")
	testutil.RunGit(t, filepath.Dir(foreign), "clone", mirrorURL, foreign)
	if err := os.WriteFile(filepath.Join(foreign, "f.txt"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, foreign, "add", "f.txt")
	testutil.RunGit(t, foreign, "commit", "--amend", "-m", "rewritten")
	testutil.RunGit(t, foreign, "push", "--force", "origin", "main")

	if err := git.PushMirror(barePath, "github"); err == nil {
		t.Error("PushMirror should fail on diverged mirror (no --force)")
	}
}
