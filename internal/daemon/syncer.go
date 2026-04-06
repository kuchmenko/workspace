package daemon

import (
	"log"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/git"
)

type Syncer struct {
	root   string
	logger *log.Logger
}

func NewSyncer(root string, logger *log.Logger) *Syncer {
	return &Syncer{root: root, logger: logger}
}

// SyncNow commits and pushes workspace.toml if the workspace is a git repo.
func (s *Syncer) SyncNow() {
	// Resolve symlinks — workspace.toml might be symlinked from dotfiles
	tomlPath := filepath.Join(s.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		s.logger.Printf("syncer: resolve symlink %s: %v", tomlPath, err)
		return
	}

	// Find the git repo that contains the real file
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		s.logger.Printf("syncer: %s is not in a git repo, skipping", s.root)
		return
	}

	if !git.HasRemote(repoRoot) {
		s.logger.Printf("syncer: %s has no remote, skipping push", repoRoot)
		return
	}

	// Get relative path of the file within the repo
	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		s.logger.Printf("syncer: rel path: %v", err)
		return
	}

	// Check if there are changes to commit
	if isClean(repoRoot, relFile) {
		s.logger.Printf("syncer: %s is clean, nothing to commit", relFile)
		return
	}

	// Add, commit, push
	if err := git.Add(repoRoot, relFile); err != nil {
		s.logger.Printf("syncer: git add: %v", err)
		return
	}

	if err := git.Commit(repoRoot, "ws: auto-sync workspace.toml"); err != nil {
		s.logger.Printf("syncer: git commit: %v", err)
		return
	}

	if err := git.Push(repoRoot); err != nil {
		s.logger.Printf("syncer: push failed, trying pull+push: %v", err)
		if err := git.Pull(repoRoot); err != nil {
			s.logger.Printf("syncer: pull also failed: %v", err)
			return
		}
		if err := git.Push(repoRoot); err != nil {
			s.logger.Printf("syncer: push after pull failed: %v", err)
			return
		}
	}

	s.logger.Printf("syncer: committed and pushed %s", relFile)
}

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
