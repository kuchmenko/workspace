package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Clone(remote, dest string) error {
	cmd := exec.Command("git", "clone", remote, dest)
	cmd.Stdout = nil
	cmd.Stderr = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

func Pull(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func IsRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func RemoteURL(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func CurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func Branches(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var branches []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			branches = append(branches, l)
		}
	}
	return branches, nil
}

func LastCommitTime(repoPath string) (time.Time, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%cI")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

func LastCommitMessage(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func HasRemote(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "remote")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func Add(repoPath, file string) error {
	cmd := exec.Command("git", "-C", repoPath, "add", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Commit(repoPath, message string) error {
	cmd := exec.Command("git", "-C", repoPath, "commit", "-m", message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Push(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "push")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// PushBranch pushes a single named branch to origin, setting upstream if
// it does not already track a remote branch.
func PushBranch(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "push", "--set-upstream", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// Fetch runs `git fetch --all --prune --tags` against repoPath. Used by the
// reconciler to refresh remote refs without touching any working tree.
func Fetch(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--all", "--prune", "--tags")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// RevParse resolves a ref to its full SHA. Returns "" on failure rather than
// erroring — callers typically want to treat "ref does not exist" as a normal
// state, not an exceptional one.
func RevParse(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// AheadBehind returns how many commits `branch` is ahead of and behind its
// upstream. Returns (0, 0, false) if the branch has no upstream configured.
func AheadBehind(repoPath, branch string) (ahead, behind int, hasUpstream bool) {
	upstream := branch + "@{u}"
	if RevParse(repoPath, upstream) == "" {
		return 0, 0, false
	}
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--left-right", "--count", upstream+"..."+branch)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, false
	}
	fmt.Sscanf(parts[0], "%d", &behind)
	fmt.Sscanf(parts[1], "%d", &ahead)
	return ahead, behind, true
}

// IsDirty reports whether repoPath has uncommitted changes (tracked or
// untracked, excluding ignored). Reconciler uses this to skip ff-pull when
// the user is mid-edit.
func IsDirty(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		// On error, err on the side of "looks dirty" so we don't accidentally
		// pull-and-overwrite a working tree we couldn't inspect.
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

// HasIndexLock reports whether .git/index.lock is present, indicating
// another git process is currently mid-write. Reconciler skips operations
// on the worktree when this is true to avoid colliding with an editor or
// interactive shell command.
func HasIndexLock(repoPath string) bool {
	gitDir := RevParse(repoPath, "--git-dir")
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}
	_, err := os.Stat(filepath.Join(gitDir, "index.lock"))
	return err == nil
}

// HasUpstream reports whether the named branch has an upstream tracking
// branch configured.
func HasUpstream(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	return cmd.Run() == nil
}

// HasStash reports whether `git stash list` has any entries. ws migrate
// uses this as a pre-flight check — stash is bound to the working .git and
// would be lost when we replace it with a worktree, unless we first
// convert each stash entry to a side branch via `git stash branch`.
func HasStash(repoPath string) bool {
	return StashCount(repoPath) > 0
}

// StashCount returns the number of entries in `git stash list`. Used by
// migrate to walk N stashes and convert each into a side branch.
func StashCount(repoPath string) int {
	cmd := exec.Command("git", "-C", repoPath, "stash", "list")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// SymbolicRef resolves a symbolic ref like refs/remotes/origin/HEAD to its
// target (e.g. "main"). Returns "" if the ref does not exist or is not symbolic.
func SymbolicRef(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ParseRepoName extracts repo name from a git remote URL.
// e.g. "git@github.com:user/repo.git" → "repo"
func ParseRepoName(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if idx := strings.LastIndex(remote, "/"); idx >= 0 {
		return remote[idx+1:]
	}
	if idx := strings.LastIndex(remote, ":"); idx >= 0 {
		return remote[idx+1:]
	}
	return remote
}

// ParseOwnerRepo extracts "owner/repo" from a git remote URL.
func ParseOwnerRepo(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	// SSH: git@github.com:owner/repo
	if idx := strings.Index(remote, ":"); idx >= 0 && !strings.Contains(remote, "://") {
		return remote[idx+1:]
	}
	// HTTPS: https://github.com/owner/repo
	parts := strings.Split(remote, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return remote
}
