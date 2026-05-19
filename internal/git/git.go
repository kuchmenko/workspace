package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

func LastCommitAuthorTime(repoPath string) (time.Time, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%aI")
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

func PushBranch(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "push", "--set-upstream", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Fetch(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--all", "--prune", "--tags")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func RevParse(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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

func IsDirty(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

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

func HasUpstream(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	return cmd.Run() == nil
}

func SetBranchUpstream(repoPath, branch, remote string) error {
	if branch == "" || remote == "" {
		return fmt.Errorf("SetBranchUpstream: empty branch or remote")
	}
	if err := setConfig(repoPath, "branch."+branch+".remote", remote); err != nil {
		return err
	}
	return setConfig(repoPath, "branch."+branch+".merge", "refs/heads/"+branch)
}

func setConfig(repoPath, key, value string) error {
	cmd := exec.Command("git", "-C", repoPath, "config", key, value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config %s=%s in %s: %s", key, value, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func HasStash(repoPath string) bool {
	return StashCount(repoPath) > 0
}

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

func SymbolicRef(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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
