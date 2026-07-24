package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
)

func findGitRoot(dir string) string {
	for {
		if git.IsRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isClean(repoPath, file string) bool {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain", file)
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) == ""
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s in %s: %s", name, strings.Join(args, " "), dir, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureUnionMerge(repoRoot, tomlAbs string) error {
	rel, err := filepath.Rel(repoRoot, tomlAbs)
	if err != nil {
		return err
	}
	attrPath := filepath.Join(repoRoot, ".gitattributes")
	wantLine := rel + " merge=union"
	existing, err := os.ReadFile(attrPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == wantLine {
			return nil
		}
	}
	f, err := os.OpenFile(attrPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = f.WriteString("\n")
	}
	if _, err := f.WriteString(wantLine + "\n"); err != nil {
		return err
	}
	_ = stageAndCommitAttr(repoRoot, attrPath)
	return nil
}

func stageAndCommitAttr(repoRoot, attrPath string) error {
	if err := runIn(repoRoot, "git", "add", attrPath); err != nil {
		return err
	}
	_ = runIn(repoRoot, "git", "commit", "-m", "chore: add union merge driver for workspace.toml")
	return nil
}

func loadMachineName() string {
	mc, err := config.LoadMachineConfig()
	if err != nil || mc == nil {
		return ""
	}
	return mc.MachineName
}

func machineHostname() string {
	if name := loadMachineName(); name != "" {
		return name
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
