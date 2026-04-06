package git

import (
	"fmt"
	"os/exec"
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
