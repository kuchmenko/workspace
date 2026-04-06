package archive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CleanableDirs are dependency/build directories that should be removed before archiving.
var CleanableDirs = []string{
	"node_modules",
	"target",
	".venv",
	"__pycache__",
	"dist",
	".next",
	".nuxt",
	".svelte-kit",
	"build",
	"vendor", // Go vendor, but check for go.mod first
	"zig-cache",
	"zig-out",
}

// CleanDeps removes dependency/build directories from a project.
func CleanDeps(projectPath string) ([]string, error) {
	var cleaned []string
	for _, dir := range CleanableDirs {
		p := filepath.Join(projectPath, dir)
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		// Special case: don't remove vendor/ if it's a Go project without go.sum
		// (might be intentionally vendored)
		if dir == "vendor" {
			if _, err := os.Stat(filepath.Join(projectPath, "go.mod")); err != nil {
				continue
			}
		}
		if err := os.RemoveAll(p); err != nil {
			return cleaned, fmt.Errorf("removing %s: %w", p, err)
		}
		cleaned = append(cleaned, dir)
	}
	return cleaned, nil
}

// TarProject creates a tar.gz archive of a project directory.
func TarProject(projectPath, archiveDir, name string) (string, error) {
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", err
	}
	archivePath := filepath.Join(archiveDir, name+".tar.gz")
	parent := filepath.Dir(projectPath)
	base := filepath.Base(projectPath)
	cmd := exec.Command("tar", "czf", archivePath, "-C", parent, base)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tar: %s", strings.TrimSpace(string(out)))
	}
	return archivePath, nil
}

// UntarProject extracts a tar.gz archive to a destination directory.
func UntarProject(archivePath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "xzf", archivePath, "-C", destDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("untar: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ArchiveExists checks if an archive file exists for a given project name.
func ArchiveExists(archiveDir, name string) bool {
	_, err := os.Stat(filepath.Join(archiveDir, name+".tar.gz"))
	return err == nil
}
