package daemon

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

type Poller struct {
	root     string
	interval time.Duration
	logger   *log.Logger
}

func NewPoller(root, interval string, logger *log.Logger) *Poller {
	dur := parseDuration(interval)
	if dur < time.Minute {
		dur = 5 * time.Minute
	}
	return &Poller{root: root, interval: dur, logger: logger}
}

func (p *Poller) Run(quit <-chan struct{}) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-quit:
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

func (p *Poller) poll() {
	// Resolve workspace.toml symlink to find the actual git repo
	tomlPath := filepath.Join(p.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return
	}

	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" || !git.HasRemote(repoRoot) {
		return
	}

	// Fetch from remote
	cmd := exec.Command("git", "-C", repoRoot, "fetch", "--quiet")
	if err := cmd.Run(); err != nil {
		p.logger.Printf("poller: fetch failed in %s: %v", repoRoot, err)
		return
	}

	// Compare HEAD with upstream
	if !hasRemoteChanges(repoRoot) {
		return
	}

	p.logger.Printf("poller: remote changes detected in %s", repoRoot)

	// Pull
	if err := git.Pull(repoRoot); err != nil {
		p.logger.Printf("poller: pull failed: %v", err)
		return
	}

	p.logger.Printf("poller: pulled changes in %s", repoRoot)

	// Reload config and sync repos
	ws, err := config.Load(p.root)
	if err != nil {
		p.logger.Printf("poller: reload config: %v", err)
		return
	}

	p.syncRepos(ws)
}

func (p *Poller) syncRepos(ws *config.Workspace) {
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		absPath := filepath.Join(p.root, proj.Path)
		if git.IsRepo(absPath) {
			continue // already cloned
		}
		// Clone missing active repos
		p.logger.Printf("poller: cloning %s → %s", name, proj.Path)
		if err := git.Clone(proj.Remote, absPath); err != nil {
			p.logger.Printf("poller: clone %s failed: %v", name, err)
		}
	}
}

func hasRemoteChanges(repoRoot string) bool {
	local := gitRevParse(repoRoot, "HEAD")
	remote := gitRevParse(repoRoot, "@{u}")
	if local == "" || remote == "" {
		return false
	}
	return local != remote
}

func gitRevParse(repoRoot, ref string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, _ := strconv.Atoi(strings.TrimSuffix(s, "d"))
		return time.Duration(days) * 24 * time.Hour
	}
	if strings.HasSuffix(s, "m") && !strings.Contains(s, "h") {
		mins, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err == nil {
			return time.Duration(mins) * time.Minute
		}
	}
	d, _ := time.ParseDuration(s)
	return d
}

func init() {
	// Avoid unused import for fmt
	_ = fmt.Sprint
}
