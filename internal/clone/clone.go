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

// Options configures one CloneIntoLayout call.
type Options struct {
	// PromptDefaultBranch is invoked when the project's default branch can
	// not be auto-detected (no proj.DefaultBranch, no origin/HEAD, no
	// well-known candidate). nil means non-interactive: the call returns
	// ErrNeedsBootstrap so the caller can record a conflict and continue.
	PromptDefaultBranch func(project string, candidates []string) (string, error)

	// Logf is the structured progress sink. nil means silent.
	Logf func(format string, args ...interface{})
}

func (o Options) logf(format string, args ...interface{}) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Result describes a successful clone.
type Result struct {
	Project       string
	BarePath      string
	MainWorktree  string
	DefaultBranch string
}

// Sentinel errors. Use errors.Is to detect.
var (
	// ErrAlreadyCloned is returned when <path>.bare already exists. Treat
	// as a no-op skip.
	ErrAlreadyCloned = errors.New("project already cloned")

	// ErrNeedsMigration is returned when <path> exists as a plain git
	// checkout (no <path>.bare sibling). The user must run `ws migrate`.
	ErrNeedsMigration = errors.New("project exists as plain clone, run 'ws migrate'")

	// ErrPathBlocked is returned when <path> exists but is not a git
	// repository — non-repo files are sitting where the worktree should go.
	ErrPathBlocked = errors.New("non-repo files present at project path")

	// ErrNeedsBootstrap is returned when default_branch can not be inferred
	// and the caller passed no PromptDefaultBranch. Surfaces as a
	// 'needs-bootstrap' conflict from the daemon path.
	ErrNeedsBootstrap = errors.New("default branch needs interactive selection")
)

// CloneIntoLayout clones proj.Remote into the canonical
// <wsRoot>/<proj.Path>.bare + <wsRoot>/<proj.Path> layout.
//
// On success, proj.DefaultBranch is filled in (if it was empty) and the
// caller is responsible for persisting workspace.toml. On failure, any
// partially-created bare repo is removed and the on-disk state matches what
// it was before the call.
func CloneIntoLayout(wsRoot, name string, proj *config.Project, opts Options) (*Result, error) {
	if proj == nil {
		return nil, fmt.Errorf("clone %s: nil project", name)
	}
	if proj.Remote == "" {
		return nil, fmt.Errorf("clone %s: empty remote", name)
	}
	if proj.Path == "" {
		return nil, fmt.Errorf("clone %s: empty path", name)
	}

	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	// Pre-flight: classify on-disk state.
	if _, err := os.Stat(barePath); err == nil {
		return nil, ErrAlreadyCloned
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", barePath, err)
	}
	if info, err := os.Stat(mainPath); err == nil {
		if info.IsDir() && git.IsRepo(mainPath) {
			return nil, ErrNeedsMigration
		}
		return nil, ErrPathBlocked
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", mainPath, err)
	}

	// Make sure the parent directory tree exists. mainPath and barePath
	// share the same parent, so one MkdirAll covers both.
	parent := filepath.Dir(barePath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create parent %s: %w", parent, err)
	}

	opts.logf("clone %s: git clone --bare %s → %s", name, proj.Remote, barePath)
	if err := git.CloneBare(proj.Remote, barePath); err != nil {
		// Bare clone failed before any state was created — nothing to roll
		// back. Surface the underlying git error verbatim so the caller can
		// show stderr to the user.
		return nil, err
	}

	// `git clone --bare` omits remote.origin.fetch. Without it, subsequent
	// `git fetch` calls update only FETCH_HEAD, branch@{u} fails to resolve,
	// and AheadBehind returns (0, 0, false) for every branch. Write the
	// standard refspec now so every downstream fetch (daemon, worktree,
	// user-initiated) updates refs/remotes/origin/* correctly.
	if err := git.SetFetchRefspec(barePath); err != nil {
		_ = os.RemoveAll(barePath)
		return nil, fmt.Errorf("set fetch refspec: %w", err)
	}

	// Resolve default_branch. After a successful `git clone --bare`, when
	// the remote exposes its HEAD, refs/remotes/origin/HEAD is set
	// automatically and we can derive the branch from it.
	defaultBranch, err := resolveDefaultBranch(name, proj, barePath, opts)
	if err != nil {
		_ = os.RemoveAll(barePath)
		return nil, err
	}
	opts.logf("clone %s: default branch = %s", name, defaultBranch)

	// Pin origin/HEAD so subsequent `git remote show origin` and similar
	// agree with what we picked. Best-effort; some remotes (or odd
	// permissions) may reject set-head and that's fine.
	_ = git.SetRemoteHead(barePath, defaultBranch)

	// Materialize the main worktree at <path> on the default branch.
	opts.logf("clone %s: worktree add %s on %s", name, mainPath, defaultBranch)
	if err := git.WorktreeAdd(barePath, mainPath, defaultBranch, ""); err != nil {
		_ = os.RemoveAll(barePath)
		// best-effort: don't leave a half-attached worktree dir
		_ = os.RemoveAll(mainPath)
		return nil, fmt.Errorf("worktree add: %w", err)
	}

	// Verify the new worktree is a real repo. If verification fails, roll
	// back so we don't leave a broken layout that confuses every other
	// command.
	if !git.IsRepo(mainPath) {
		_ = os.RemoveAll(mainPath)
		_ = os.RemoveAll(barePath)
		return nil, fmt.Errorf("verification failed: %s is not a git repo after worktree add", mainPath)
	}

	// Wire up upstream tracking for the default branch so plain `git push`
	// and `git pull` work in the new main worktree without arguments.
	// SetBranchUpstream writes branch.<name>.remote and branch.<name>.merge
	// directly instead of calling `git branch --set-upstream-to=origin/<name>`,
	// which would require a second fetch first: we just cloned and haven't
	// populated refs/remotes/origin/* yet (the refspec set above takes
	// effect on the next fetch). Best-effort: if this fails the clone is
	// still usable, just ergonomically annoying.
	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("clone %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}

	proj.DefaultBranch = defaultBranch

	return &Result{
		Project:       name,
		BarePath:      barePath,
		MainWorktree:  mainPath,
		DefaultBranch: defaultBranch,
	}, nil
}

// resolveDefaultBranch picks the project's default branch using:
//
//  1. proj.DefaultBranch if already set
//  2. refs/remotes/origin/HEAD inside the freshly cloned bare
//  3. well-known candidates (main, master, trunk) that exist locally
//  4. opts.PromptDefaultBranch — if nil, returns ErrNeedsBootstrap
//
// Step 4 is the only step that can return ErrNeedsBootstrap, and only when
// the caller is non-interactive.
func resolveDefaultBranch(name string, proj *config.Project, barePath string, opts Options) (string, error) {
	if proj.DefaultBranch != "" {
		return proj.DefaultBranch, nil
	}
	if br := git.SymbolicRef(barePath, "refs/remotes/origin/HEAD"); br != "" {
		// "origin/main" → "main"
		if i := strings.Index(br, "/"); i >= 0 {
			return br[i+1:], nil
		}
		return br, nil
	}
	var candidates []string
	for _, c := range []string{"main", "master", "trunk"} {
		if git.HasBranch(barePath, c) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if opts.PromptDefaultBranch == nil {
		return "", ErrNeedsBootstrap
	}
	picked, err := opts.PromptDefaultBranch(name, candidates)
	if err != nil {
		return "", err
	}
	picked = strings.TrimSpace(picked)
	if picked == "" {
		return "", fmt.Errorf("no default branch selected for %s", name)
	}
	return picked, nil
}
