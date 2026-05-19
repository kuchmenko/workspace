package migrate

import (
	"fmt"
	"os/exec"
	"strings"
)

func runGit(repoPath string, args ...string) error {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}
