// Package clone materializes a project from its remote URL directly into the
// bare+worktree layout that the workspace uses everywhere else.
//
// This is the shared primitive behind:
//
//   - `ws bootstrap`     — interactive, walks workspace.toml on a fresh machine
//   - daemon reconciler  — auto-clones missing projects on each tick
//   - `ws add` (future)  — registers a new project and clones it in one shot
//
// CloneIntoLayout is intentionally narrow: it owns the filesystem dance and
// the default-branch resolution, but it leaves persistence of workspace.toml
// to the caller. Callers must save the (possibly mutated) Project after a
// successful return.
package clone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type Options struct {
	PromptDefaultBranch func(project string, candidates []string) (string, error)

	Logf func(format string, args ...interface{})
}

func (o Options) logf(format string, args ...interface{}) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

type Result struct {
	Project       string
	BarePath      string
	MainWorktree  string
	DefaultBranch string
}

var (
	ErrAlreadyCloned = errors.New("project already cloned")

	ErrNeedsMigration = errors.New("project exists as plain clone, run 'ws migrate'")

	ErrPathBlocked = errors.New("non-repo files present at project path")

	ErrNeedsBootstrap = errors.New("default branch needs interactive selection")
)

func CloneIntoLayout(wsRoot, name string, proj *config.Project, opts Options) (*Result, error) {
	if err := validateCloneInputs(name, proj); err != nil {
		return nil, err
	}
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)
	if err := preflightLayout(barePath, mainPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		return nil, fmt.Errorf("create parent %s: %w", filepath.Dir(barePath), err)
	}
	opts.logf("clone %s: git clone --bare %s → %s", name, proj.Remote, barePath)
	if err := git.CloneBare(proj.Remote, barePath); err != nil {
		return nil, err
	}
	defaultBranch, err := initBareLayout(name, proj, barePath, opts)
	if err != nil {
		_ = os.RemoveAll(barePath)
		return nil, err
	}
	if err := materializeMainWorktree(name, barePath, mainPath, defaultBranch, opts); err != nil {
		return nil, err
	}
	proj.DefaultBranch = defaultBranch
	return &Result{
		Project:       name,
		BarePath:      barePath,
		MainWorktree:  mainPath,
		DefaultBranch: defaultBranch,
	}, nil
}

func validateCloneInputs(name string, proj *config.Project) error {
	if proj == nil {
		return fmt.Errorf("clone %s: nil project", name)
	}
	if proj.Remote == "" {
		return fmt.Errorf("clone %s: empty remote", name)
	}
	if proj.Path == "" {
		return fmt.Errorf("clone %s: empty path", name)
	}
	return nil
}

func preflightLayout(barePath, mainPath string) error {
	if _, err := os.Stat(barePath); err == nil {
		return ErrAlreadyCloned
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", barePath, err)
	}
	info, err := os.Stat(mainPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", mainPath, err)
	}
	if info.IsDir() && git.IsRepo(mainPath) {
		return ErrNeedsMigration
	}
	return ErrPathBlocked
}

func initBareLayout(name string, proj *config.Project, barePath string, opts Options) (string, error) {
	if err := git.SetFetchRefspec(barePath); err != nil {
		return "", fmt.Errorf("set fetch refspec: %w", err)
	}
	defaultBranch, err := resolveDefaultBranch(name, proj, barePath, opts)
	if err != nil {
		return "", err
	}
	opts.logf("clone %s: default branch = %s", name, defaultBranch)

	_ = git.SetRemoteHead(barePath, defaultBranch)
	return defaultBranch, nil
}

func materializeMainWorktree(name, barePath, mainPath, defaultBranch string, opts Options) error {
	opts.logf("clone %s: worktree add %s on %s", name, mainPath, defaultBranch)
	if err := git.WorktreeAdd(barePath, mainPath, defaultBranch, ""); err != nil {
		_ = os.RemoveAll(barePath)
		_ = os.RemoveAll(mainPath)
		return fmt.Errorf("worktree add: %w", err)
	}
	if !git.IsRepo(mainPath) {
		_ = os.RemoveAll(mainPath)
		_ = os.RemoveAll(barePath)
		return fmt.Errorf("verification failed: %s is not a git repo after worktree add", mainPath)
	}

	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("clone %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}
	return nil
}

func resolveDefaultBranch(name string, proj *config.Project, barePath string, opts Options) (string, error) {
	if proj.DefaultBranch != "" {
		return proj.DefaultBranch, nil
	}
	if br := defaultBranchFromOriginHEAD(barePath); br != "" {
		return br, nil
	}
	candidates := wellKnownDefaultCandidates(barePath)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if opts.PromptDefaultBranch == nil {
		return "", ErrNeedsBootstrap
	}
	return promptForDefaultBranch(name, candidates, opts.PromptDefaultBranch)
}

func defaultBranchFromOriginHEAD(barePath string) string {
	br := git.SymbolicRef(barePath, "refs/remotes/origin/HEAD")
	if br == "" {
		return ""
	}
	if i := strings.Index(br, "/"); i >= 0 {
		return br[i+1:]
	}
	return br
}

func wellKnownDefaultCandidates(barePath string) []string {
	var out []string
	for _, c := range []string{"main", "master", "trunk"} {
		if git.HasBranch(barePath, c) {
			out = append(out, c)
		}
	}
	return out
}

func promptForDefaultBranch(name string, candidates []string, prompt func(string, []string) (string, error)) (string, error) {
	picked, err := prompt(name, candidates)
	if err != nil {
		return "", err
	}
	picked = strings.TrimSpace(picked)
	if picked == "" {
		return "", fmt.Errorf("no default branch selected for %s", name)
	}
	return picked, nil
}
