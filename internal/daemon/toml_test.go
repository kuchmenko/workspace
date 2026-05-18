package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/testutil"
)

// TestSyncTOMLAmendCooldownSquashesAutoSyncCommits is the regression test for
// the issue that motivated the cooldown gate: the daemon used to stack one
// "ws: auto-sync workspace.toml from <host>" commit per tick, flooding the
// dotfiles history. With a positive pushCooldown, successive auto-sync
// commits should amend into a single local commit and only push once the
// cooldown elapses (here, simulated by flipping cooldown to 0).
func TestSyncTOMLAmendCooldownSquashesAutoSyncCommits(t *testing.T) {
	wsRoot, bareDir := setupSyncTOMLRepo(t)

	r := NewReconciler(wsRoot, 5*time.Minute, log.New(io.Discard, "", 0))
	r.SetPushCooldown(time.Hour)

	remoteHead := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main")

	// Edit 1 → commit, hold (do not push).
	appendFile(t, filepath.Join(wsRoot, "workspace.toml"), "# edit 1\n")
	if _, err := r.syncTOML(); err != nil {
		t.Fatalf("syncTOML edit 1: %v", err)
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("remote moved after first auto-sync; cooldown should have held the push")
	}
	if msg := testutil.RunGit(t, wsRoot, "log", "-1", "--format=%s"); !strings.HasPrefix(msg, "ws: auto-sync workspace.toml from ") {
		t.Fatalf("HEAD message %q is not an auto-sync commit", msg)
	}
	if a := countAhead(t, wsRoot); a != 1 {
		t.Fatalf("expected ahead=1 after first edit, got %d", a)
	}
	firstHead := testutil.RunGit(t, wsRoot, "rev-parse", "HEAD")

	// Edit 2 → amend onto the held commit (HEAD sha changes, ahead stays 1).
	appendFile(t, filepath.Join(wsRoot, "workspace.toml"), "# edit 2\n")
	if _, err := r.syncTOML(); err != nil {
		t.Fatalf("syncTOML edit 2: %v", err)
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("remote moved after amended auto-sync; cooldown should still hold")
	}
	if a := countAhead(t, wsRoot); a != 1 {
		t.Fatalf("expected ahead=1 after amend, got %d", a)
	}
	secondHead := testutil.RunGit(t, wsRoot, "rev-parse", "HEAD")
	if secondHead == firstHead {
		t.Fatalf("HEAD should change on amend (new tree); stayed at %s", firstHead)
	}
	content, err := os.ReadFile(filepath.Join(wsRoot, "workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# edit 1") || !strings.Contains(string(content), "# edit 2") {
		t.Fatalf("amended commit lost an edit; tree contents:\n%s", content)
	}

	// Cooldown elapsed (simulated via SetPushCooldown(0)) → next tick must
	// push the held commit even though the working tree is clean.
	r.SetPushCooldown(0)
	if _, err := r.syncTOML(); err != nil {
		t.Fatalf("syncTOML post-cooldown push: %v", err)
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != secondHead {
		t.Fatalf("expected remote at %s, got %s", secondHead, got)
	}
	if a := countAhead(t, wsRoot); a != 0 {
		t.Fatalf("expected ahead=0 after push, got %d", a)
	}
}

// TestSyncTOMLZeroCooldownPushesEveryCommit pins the legacy behaviour for
// `ws sync` (cooldown 0): every dirty edit produces its own commit and is
// pushed immediately, no amend.
func TestSyncTOMLZeroCooldownPushesEveryCommit(t *testing.T) {
	wsRoot, bareDir := setupSyncTOMLRepo(t)

	r := NewReconciler(wsRoot, 5*time.Minute, log.New(io.Discard, "", 0))
	// Default pushCooldown is 0; assert that explicitly so the test does not
	// silently depend on NewReconciler's choice.
	r.SetPushCooldown(0)

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
		t.Fatalf("expected a fresh commit (not amend) with cooldown=0; HEAD unchanged at %s", headA)
	}
	if got := testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/main"); got != headB {
		t.Fatalf("expected immediate push to %s, remote at %s", headB, got)
	}
}

// setupSyncTOMLRepo builds a workspace clone wired to a bare upstream with a
// seeded workspace.toml committed on main. Returns (wsRoot, bareDir).
func setupSyncTOMLRepo(t *testing.T) (string, string) {
	t.Helper()
	bareDir := testutil.InitFakeRemote(t, "ws-toml", "main")

	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "ws")
	testutil.RunGit(t, tmp, "clone", bareDir, "ws")

	// Pin local identity / disable signing so the reconciler's plain
	// exec.Command("git", ...) calls do not depend on the developer's
	// global git config.
	testutil.RunGit(t, wsRoot, "config", "user.name", "ws-test")
	testutil.RunGit(t, wsRoot, "config", "user.email", "test@example.invalid")
	testutil.RunGit(t, wsRoot, "config", "commit.gpgsign", "false")
	testutil.RunGit(t, wsRoot, "config", "tag.gpgsign", "false")

	// Seed workspace.toml so syncTOML has a tracked file to watch.
	tomlPath := filepath.Join(wsRoot, "workspace.toml")
	if err := os.WriteFile(tomlPath, []byte("# seeded workspace.toml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, wsRoot, "add", "workspace.toml")
	testutil.RunGit(t, wsRoot, "commit", "-m", "seed workspace.toml")
	testutil.RunGit(t, wsRoot, "push", "-u", "origin", "main")

	return wsRoot, bareDir
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func countAhead(t *testing.T, repo string) int {
	t.Helper()
	out := testutil.RunGit(t, repo, "rev-list", "--count", "@{u}..HEAD")
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("parse ahead count %q: %v", out, err)
	}
	return n
}
