package cli

import (
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestCheckMirrorRemotes_MissingAndFix(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	mirrorURL := filepath.Join(t.TempDir(), "mirror.git")
	testutil.RunGit(t, filepath.Dir(mirrorURL), "init", "--bare", "--initial-branch=main", mirrorURL)
	proj.Mirrors = map[string]string{"github": mirrorURL}

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})

	fs := r.checkMirrorRemotes("demo", proj, barePath)
	if len(fs) != 1 || fs[0].Severity != Error {
		t.Fatalf("want 1 Error finding for missing mirror, got %+v", fs)
	}
	if fs[0].Fix == nil {
		t.Fatal("missing mirror must offer an auto-fix")
	}
	if err := fs[0].Fix(); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	after := r.checkMirrorRemotes("demo", proj, barePath)
	if len(after) != 1 || after[0].Severity != OK {
		t.Fatalf("after Fix want OK, got %+v", after)
	}
	if !git.MirrorRemoteOK(barePath, "github", mirrorURL) {
		t.Error("Fix did not install the mirror remote correctly")
	}
}

func TestCheckMirrorRemotes_ExtraRemoteWarns(t *testing.T) {
	wsRoot := t.TempDir()
	proj, barePath := makeProjectBare(t, wsRoot, "demo", "main")
	testutil.RunGit(t, barePath, "remote", "add", "stray", "/tmp/nowhere.git")
	proj.Mirrors = map[string]string{}

	r := newRunnerFor(t, wsRoot, map[string]config.Project{"demo": proj})

	// No declared mirrors → no findings at all, even with a stray remote.
	if fs := r.checkMirrorRemotes("demo", proj, barePath); fs != nil {
		t.Fatalf("no declared mirrors should emit nothing, got %+v", fs)
	}

	// With a declared mirror present, the stray remote gets a Warn.
	mirrorURL := filepath.Join(t.TempDir(), "mirror.git")
	testutil.RunGit(t, filepath.Dir(mirrorURL), "init", "--bare", "--initial-branch=main", mirrorURL)
	proj.Mirrors = map[string]string{"github": mirrorURL}
	if err := git.EnsureMirrorRemote(barePath, "github", mirrorURL); err != nil {
		t.Fatalf("EnsureMirrorRemote: %v", err)
	}

	fs := r.checkMirrorRemotes("demo", proj, barePath)
	var warned bool
	for _, f := range fs {
		if f.Check == "mirror:extra" && f.Severity == Warn {
			warned = true
		}
		if f.Check == "mirror:github" && f.Severity != OK {
			t.Errorf("declared mirror should be OK, got %s: %s", f.Severity, f.Message)
		}
	}
	if !warned {
		t.Error("stray remote should produce a mirror:extra Warn")
	}
}
