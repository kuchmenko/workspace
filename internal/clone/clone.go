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

// validateCloneInputs enforces the not-nil + not-empty preconditions
// on a project before any disk IO. Each error names the specific
// missing field so callers can show the user what's wrong.
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

// preflightLayout classifies the on-disk state at the bare and main
// paths so we know which sentinel error (if any) to return before
// kicking off the clone:
//
//   - bare exists                       → ErrAlreadyCloned
//   - main exists & is repo             → ErrNeedsMigration
//   - main exists but is not a repo     → ErrPathBlocked
//   - any unexpected stat error         → wrapped IO error
//   - both missing                      → nil (caller proceeds)
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

// initBareLayout finishes the bare-side setup that has to land before
// the main worktree can be created: writes the fetch refspec, picks
// the default branch, and pins origin/HEAD. Returns the default
// branch on success.
func initBareLayout(name string, proj *config.Project, barePath string, opts Options) (string, error) {
	// `git clone --bare` omits remote.origin.fetch. Without it, subsequent
	// `git fetch` calls update only FETCH_HEAD, branch@{u} fails to resolve,
	// and AheadBehind returns (0, 0, false) for every branch.
	if err := git.SetFetchRefspec(barePath); err != nil {
		return "", fmt.Errorf("set fetch refspec: %w", err)
	}
	defaultBranch, err := resolveDefaultBranch(name, proj, barePath, opts)
	if err != nil {
		return "", err
	}
	opts.logf("clone %s: default branch = %s", name, defaultBranch)
	// Pin origin/HEAD so subsequent `git remote show origin` and similar
	// agree with what we picked. Best-effort.
	_ = git.SetRemoteHead(barePath, defaultBranch)
	return defaultBranch, nil
}

// materializeMainWorktree creates the main worktree at mainPath on
// defaultBranch, verifies it, and wires up upstream tracking. Cleans
// up both bare and main on failure so a partial state never lingers.
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
	// SetBranchUpstream writes branch.<name>.remote / .merge directly
	// instead of `git branch --set-upstream-to=origin/<name>`, which
	// would need a second fetch first (we just cloned and haven't
	// populated refs/remotes/origin/* yet). Best-effort: a failure
	// here leaves the clone usable, just ergonomically annoying.
	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("clone %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}
	return nil
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

// defaultBranchFromOriginHEAD reads refs/remotes/origin/HEAD and
// returns the bare branch name (strips the "origin/" prefix).
// Returns "" when the symbolic ref is not set.
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

// wellKnownDefaultCandidates returns the subset of the main /
// master / trunk branch names that exist locally in the bare repo.
// One match is "definitely the default"; zero or multiple kicks
// the resolver into prompt-mode.
func wellKnownDefaultCandidates(barePath string) []string {
	var out []string
	for _, c := range []string{"main", "master", "trunk"} {
		if git.HasBranch(barePath, c) {
			out = append(out, c)
		}
	}
	return out
}

// promptForDefaultBranch invokes the caller-supplied prompt and
// validates the result is a non-empty trimmed string.
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
