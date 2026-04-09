package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// CloneBare clones `remote` into `dest` as a bare repository. The destination
// must not already exist. Used by `ws add` (network clone) and by `ws migrate`
// (when wrapping an existing checkout, see CloneBareLocal).
func CloneBare(remote, dest string) error {
	cmd := exec.Command("git", "clone", "--bare", remote, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

// CloneBareLocal clones a local plain repo into a bare repo without going
// through the network. --no-local is used so git copies all objects rather
// than hardlinking them — important because the source .git is going to be
// deleted by the migration step that follows.
func CloneBareLocal(srcRepoPath, destBarePath string) error {
	cmd := exec.Command("git", "clone", "--bare", "--no-local", srcRepoPath, destBarePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare --no-local %s: %s", srcRepoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsBare reports whether path is a bare git repository.
func IsBare(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// SetRemoteURL points origin at a new URL. Used post-CloneBareLocal so the
// freshly created bare repo talks to the actual remote, not the local source.
func SetRemoteURL(repoPath, url string) error {
	// First try to update an existing origin; if that fails, add it.
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-url", "origin", url)
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("git", "-C", repoPath, "remote", "add", "origin", url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetRemoteHead pins origin/HEAD to the named branch. Used during migration
// so the bare repo knows what the project's default branch is.
func SetRemoteHead(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-head", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote head %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// FetchRefspec fetches a specific refspec from a source repo into the current
// repo. Used by migration to ensure local-only branches make it into the bare.
func FetchRefspec(repoPath, source, refspec string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", source, refspec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s %s in %s: %s", source, refspec, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

// HasBranch reports whether refs/heads/<branch> exists in the repo.
func HasBranch(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// RenameBranch renames a local branch. Works on both bare and non-bare repos.
func RenameBranch(repoPath, oldName, newName string) error {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-m", oldName, newName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -m %s %s: %s", oldName, newName, strings.TrimSpace(string(out)))
	}
	return nil
}
