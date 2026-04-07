package migrate

import (
	"fmt"
	"os/exec"
	"strings"
)

// runGit runs a git command in repoPath and discards stdout. The migration
// step uses this for state-mutating commands (checkout, add, commit) that
// don't already have a wrapper in internal/git — the goal is to keep
// internal/git focused on stable, reusable wrappers and let one-shot
// migration plumbing live here.
func runGit(repoPath string, args ...string) error {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func runGitOut(repoPath string, args ...string) (string, error) {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
