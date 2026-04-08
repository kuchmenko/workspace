// Package testutil provides shared test fixtures for the workspace tests.
//
// The strategy throughout the project's tests is "real git in temp dirs":
// every test creates its own ephemeral git repos under t.TempDir() and runs
// real git commands against them. This catches the kinds of bugs (like the
// `git worktree add --force` regression) that mock-based tests would miss
// because the mock would have to lie about what real git accepts.
//
// All helpers fail the test on error via t.Fatalf — no error returns —
// because fixture setup failures are bugs in the test, not the code under
// test.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitConfig returns a minimal git env that prevents the user's global
// config from leaking in (commit signing, GPG, hooks, identity). Tests need
// determinism above all else.
func GitConfig() []string {
	return []string{
		"GIT_AUTHOR_NAME=ws-test",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=ws-test",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + os.TempDir(), // last-resort fallback for any tool that ignores GIT_CONFIG_GLOBAL
		"PATH=" + os.Getenv("PATH"),
	}
}

// RunGit executes `git -C dir args...` with the test git env. Fails the
// test on non-zero exit, including stderr in the failure message.
func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = GitConfig()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// RunGitTry is like RunGit but returns the error instead of failing.
// Useful for tests that exercise expected failure modes.
func RunGitTry(t *testing.T, dir string, args ...string) error {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = GitConfig()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &gitError{args: args, dir: dir, output: string(out), inner: err}
	}
	return nil
}

type gitError struct {
	args   []string
	dir    string
	output string
	inner  error
}

func (e *gitError) Error() string {
	return "git " + strings.Join(e.args, " ") + " in " + e.dir + ": " + e.inner.Error() + "\n" + e.output
}

// InitFakeRemote creates a bare git repo at t.TempDir()/<name>.git, seeds
// it with one commit on `defaultBranch`, and returns its absolute path.
// Used as a fake `proj.Remote` for clone tests. The bare is also configured
// with HEAD → defaultBranch so origin/HEAD can be auto-detected by callers.
func InitFakeRemote(t *testing.T, name, defaultBranch string) string {
	t.Helper()
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, name+".git")
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bareDir, err)
	}
	RunGit(t, bareDir, "init", "--bare", "--initial-branch="+defaultBranch)

	// Seed: init a separate work dir, commit, push to bare. We cannot
	// `clone --branch <main>` from an empty bare because the branch
	// doesn't exist yet, hence the manual init+push.
	seedDir := filepath.Join(tmp, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", seedDir, err)
	}
	RunGit(t, seedDir, "init", "--initial-branch="+defaultBranch)
	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	RunGit(t, seedDir, "add", "README.md")
	RunGit(t, seedDir, "commit", "-m", "initial")
	RunGit(t, seedDir, "remote", "add", "origin", bareDir)
	RunGit(t, seedDir, "push", "-u", "origin", defaultBranch)

	// Pin HEAD in the bare so origin/HEAD resolves for clone tests.
	RunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch)
	return bareDir
}

// InitFakePlainCheckout creates a non-bare git repo at parent/<name> with
// the given branches. The first branch in `branches` is the default. Each
// branch gets one unique commit so the bare clone preserves real history.
//
// The returned path is the working tree root (parent/name).
func InitFakePlainCheckout(t *testing.T, parent, name string, branches []string) string {
	t.Helper()
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	RunGit(t, dir, "init", "--initial-branch="+branches[0])

	// Seed initial commit on the default branch.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	RunGit(t, dir, "add", "README.md")
	RunGit(t, dir, "commit", "-m", "initial")

	// Create extra branches with one commit each.
	for _, b := range branches[1:] {
		RunGit(t, dir, "checkout", "-b", b)
		fname := filepath.Join(dir, b+".txt")
		if err := os.WriteFile(fname, []byte(b), 0o644); err != nil {
			t.Fatalf("write %s: %v", fname, err)
		}
		RunGit(t, dir, "add", b+".txt")
		RunGit(t, dir, "commit", "-m", "branch "+b)
	}
	// Return to the default branch so callers see a familiar starting state.
	RunGit(t, dir, "checkout", branches[0])
	return dir
}

// AddDirty makes the working tree of repoPath dirty by writing an
// untracked file. Used by migrate tests to exercise the dirty-tree path.
func AddDirty(t *testing.T, repoPath string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoPath, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	RunGit(t, repoPath, "add", "uncommitted.txt")
}

// AddStash creates a stash entry in repoPath. Requires the repo to be
// non-empty (have at least one commit). Returns after restoring a clean
// working tree.
func AddStash(t *testing.T, repoPath string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoPath, "stash-me.txt"), []byte("stash\n"), 0o644); err != nil {
		t.Fatalf("write stash file: %v", err)
	}
	RunGit(t, repoPath, "add", "stash-me.txt")
	RunGit(t, repoPath, "stash", "push", "-m", "test stash")
}
