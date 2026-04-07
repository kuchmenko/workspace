// Package migrate converts plain `git clone` checkouts under a workspace
// into the worktree-based layout (bare repo + main worktree sibling).
//
// The migration is intentionally fail-safe rather than reversible: there is
// no `ws unmigrate`, but every step before the irreversible final swap
// preserves the original .git so the user can recover by hand. See
// MigrateProject for the precise ordering.
package migrate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// Options controls a migration run.
type Options struct {
	// WIP: when true, dirty working trees are auto-committed to a
	// wt/<machine>/migration-wip-<ts> branch instead of aborting.
	WIP bool
	// Machine is the sanitized machine name for branch namespacing. Required
	// when WIP is true; otherwise unused.
	Machine string
	// PromptDefaultBranch is invoked when the project's default branch can
	// not be determined automatically. Implementations should pick from
	// `candidates` (which may be empty) or return a free-form branch name.
	// Returning an error aborts the migration.
	PromptDefaultBranch func(project string, candidates []string) (string, error)
	// Logf is the structured progress sink. nil means silent.
	Logf func(format string, args ...interface{})
}

func (o Options) logf(format string, args ...interface{}) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Result describes the outcome of migrating one project.
type Result struct {
	Project        string
	BarePath       string
	MainWorktree   string
	DefaultBranch  string
	HooksMigrated  []string
	WIPBranch      string // non-empty when --wip created a snapshot branch
	WIPWorktree    string // non-empty when --wip created an extra worktree
	BranchesPushed int    // count of local branches preserved into bare
}

// ErrAlreadyMigrated is returned when the project already has a sibling .bare
// directory. Callers (notably MigrateAll) should treat this as a skip, not
// a hard error.
var ErrAlreadyMigrated = errors.New("project already migrated")

// CheckResult reports the migration-related state of one project without
// making any changes.
type CheckResult struct {
	Project   string
	State     string // "migrated" | "needs-migration" | "missing" | "not-a-repo"
	MainPath  string
	BarePath  string
	HasStash  bool
	IsDirty   bool
	Detached  bool
	Branch    string
	HooksFound int
}

// Check inspects a project on disk and reports its layout state without
// touching anything. Useful for `ws migrate --check`.
func Check(wsRoot string, name string, proj config.Project) CheckResult {
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)
	res := CheckResult{Project: name, MainPath: mainPath, BarePath: barePath}

	if _, err := os.Stat(barePath); err == nil {
		res.State = "migrated"
		return res
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		res.State = "missing"
		return res
	}
	if !git.IsRepo(mainPath) {
		res.State = "not-a-repo"
		return res
	}
	res.State = "needs-migration"
	res.HasStash = git.HasStash(mainPath)
	res.IsDirty = git.IsDirty(mainPath)
	if br, _ := git.CurrentBranch(mainPath); br == "" {
		res.Detached = true
	} else {
		res.Branch = br
	}
	hooks, _ := listActiveHooks(filepath.Join(mainPath, ".git", "hooks"))
	res.HooksFound = len(hooks)
	return res
}

// MigrateProject runs the full migration for one named project. The caller
// owns the workspace.toml save: this function may mutate `proj` to fill in
// DefaultBranch, but it does not write the file.
func MigrateProject(wsRoot string, name string, proj *config.Project, opts Options) (*Result, error) {
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	// Step 1: validate
	if _, err := os.Stat(barePath); err == nil {
		return nil, ErrAlreadyMigrated
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("project path %s does not exist", mainPath)
	}
	if !git.IsRepo(mainPath) {
		return nil, fmt.Errorf("%s is not a git repo", mainPath)
	}
	if git.IsBare(mainPath) {
		return nil, fmt.Errorf("%s is already a bare repo (unexpected layout)", mainPath)
	}

	opts.logf("migrate %s: starting at %s", name, mainPath)

	// Step 2: determine default_branch
	defaultBranch, err := resolveDefaultBranch(name, proj, mainPath, opts)
	if err != nil {
		return nil, err
	}
	opts.logf("migrate %s: default branch = %s", name, defaultBranch)

	// Step 3: pre-flight — stash + dirty
	if git.HasStash(mainPath) {
		return nil, fmt.Errorf("%s has stash entries; pop or drop them first (stash is bound to the old .git and would be lost)", name)
	}
	// Capture the original branch BEFORE any WIP shenanigans so that the
	// post-migration main worktree ends up where the user left it, not on
	// the snapshot branch.
	originalBranch, _ := git.CurrentBranch(mainPath)
	if originalBranch == "" {
		return nil, fmt.Errorf("%s is in detached HEAD; check out a branch first", name)
	}

	dirty := git.IsDirty(mainPath)
	wipBranch := ""
	wipTopic := ""
	if dirty {
		if !opts.WIP {
			return nil, fmt.Errorf("%s has uncommitted changes; commit them or re-run with --wip to snapshot to a wt/<machine>/migration-wip branch", name)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("--wip requires a configured machine name")
		}
		wipTopic = fmt.Sprintf("migration-wip-%d", time.Now().Unix())
		wipBranch = layout.BranchName(opts.Machine, wipTopic)
		opts.logf("migrate %s: dirty tree → snapshot to %s", name, wipBranch)
		if err := runGit(mainPath, "checkout", "-b", wipBranch); err != nil {
			return nil, fmt.Errorf("create WIP branch: %w", err)
		}
		if err := runGit(mainPath, "add", "-A"); err != nil {
			return nil, fmt.Errorf("stage WIP: %w", err)
		}
		if err := runGit(mainPath, "commit", "-m", "ws: migration WIP snapshot"); err != nil {
			return nil, fmt.Errorf("commit WIP: %w", err)
		}
		// Return main worktree to the original branch so the post-migration
		// state matches what the user expects. The WIP commit lives only on
		// the WIP branch, which becomes a sibling worktree below.
		if err := runGit(mainPath, "checkout", originalBranch); err != nil {
			return nil, fmt.Errorf("restore original branch %s: %w", originalBranch, err)
		}
	}

	// Step 4: capture state
	currentBranch := originalBranch
	localBranches, err := git.Branches(mainPath)
	if err != nil {
		return nil, fmt.Errorf("list local branches: %w", err)
	}
	upstreams := map[string]string{}
	for _, b := range localBranches {
		if u := upstreamFor(mainPath, b); u != "" {
			upstreams[b] = u
		}
	}
	originalHead := git.RevParse(mainPath, "HEAD")
	if originalHead == "" {
		return nil, fmt.Errorf("could not resolve HEAD in %s", mainPath)
	}

	// Step 5: detect hooks (pre-bare so we read from the original .git)
	hooksDir := filepath.Join(mainPath, ".git", "hooks")
	activeHooks, _ := listActiveHooks(hooksDir)
	if len(activeHooks) > 0 {
		opts.logf("migrate %s: found %d active hook(s): %s", name, len(activeHooks), strings.Join(activeHooks, ", "))
	}

	// Step 6: clone --bare --no-local into sibling
	opts.logf("migrate %s: cloning bare → %s", name, barePath)
	if err := git.CloneBareLocal(mainPath, barePath); err != nil {
		return nil, err
	}

	// Step 7: ensure all local branches exist in bare. clone --bare copies
	// HEAD's branch and everything reachable via refs, but a branch with no
	// upstream and no other refs pointing at it can theoretically be missed
	// in pathological cases. Belt and suspenders.
	for _, b := range localBranches {
		if git.HasBranch(barePath, b) {
			continue
		}
		opts.logf("migrate %s: backfilling missing branch %s into bare", name, b)
		if err := git.FetchRefspec(barePath, mainPath, b+":refs/heads/"+b); err != nil {
			rollbackBare(barePath)
			return nil, fmt.Errorf("backfill branch %s: %w", b, err)
		}
	}

	// Step 8: point bare at the actual remote (clone --no-local set origin
	// to mainPath, which is about to disappear) and fetch.
	if proj.Remote != "" {
		if err := git.SetRemoteURL(barePath, proj.Remote); err != nil {
			rollbackBare(barePath)
			return nil, err
		}
		if err := git.Fetch(barePath); err != nil {
			// Network failure here is recoverable — we still have a valid
			// bare with all local objects. Just log and continue.
			opts.logf("migrate %s: warning: initial fetch failed: %v", name, err)
		}
		// best-effort: pin origin/HEAD to default_branch
		_ = git.SetRemoteHead(barePath, defaultBranch)
	}

	// Step 9: restore upstream tracking (clone --no-local doesn't carry
	// branch.<name>.remote/merge config)
	for branch, upstream := range upstreams {
		if err := git.SetUpstream(barePath, branch, upstream); err != nil {
			opts.logf("migrate %s: warning: could not set upstream %s for %s: %v", name, upstream, branch, err)
		}
	}

	// Step 10: migrate hooks
	migratedHooks, err := copyHooks(hooksDir, filepath.Join(barePath, "hooks"), activeHooks)
	if err != nil {
		opts.logf("migrate %s: warning: hook migration partial: %v", name, err)
	}

	// Step 11: replace working dir's .git with a worktree pointer.
	//
	// Strategy: move .git aside (not delete), then `git worktree add --force`
	// the existing path. If anything goes wrong before the final cleanup,
	// the original .git is still recoverable by hand.
	movedGit := filepath.Join(mainPath, fmt.Sprintf(".git.migrating-%d", time.Now().Unix()))
	if err := os.Rename(filepath.Join(mainPath, ".git"), movedGit); err != nil {
		rollbackBare(barePath)
		return nil, fmt.Errorf("move .git aside: %w", err)
	}

	if err := git.WorktreeAddExisting(barePath, mainPath, currentBranch); err != nil {
		// rollback
		_ = os.Rename(movedGit, filepath.Join(mainPath, ".git"))
		rollbackBare(barePath)
		return nil, fmt.Errorf("attach worktree: %w", err)
	}

	// Verify the new worktree is functional and points at the same HEAD.
	if !git.IsRepo(mainPath) {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git")) // remove broken worktree pointer
		_ = os.Rename(movedGit, filepath.Join(mainPath, ".git"))
		rollbackBare(barePath)
		return nil, fmt.Errorf("worktree verification failed: %s is no longer a git repo", mainPath)
	}
	if newHead := git.RevParse(mainPath, "HEAD"); newHead != originalHead {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		_ = os.Rename(movedGit, filepath.Join(mainPath, ".git"))
		rollbackBare(barePath)
		return nil, fmt.Errorf("worktree verification failed: HEAD shifted from %s to %s", originalHead, newHead)
	}

	// Verification passed — irreversible step.
	if err := os.RemoveAll(movedGit); err != nil {
		opts.logf("migrate %s: warning: could not remove %s: %v", name, movedGit, err)
	}

	// Step 12: if WIP was created, attach it as a sibling worktree so the
	// user can find their snapshot.
	wipWorktree := ""
	if wipBranch != "" {
		wipWorktree = layout.WorktreePath(mainPath, opts.Machine, wipTopic)
		if err := git.WorktreeAdd(barePath, wipWorktree, wipBranch, ""); err != nil {
			opts.logf("migrate %s: warning: could not create WIP worktree: %v", name, err)
			wipWorktree = ""
		}
	}

	// Mutate proj so the caller can persist default_branch.
	proj.DefaultBranch = defaultBranch

	opts.logf("migrate %s: done", name)

	return &Result{
		Project:        name,
		BarePath:       barePath,
		MainWorktree:   mainPath,
		DefaultBranch:  defaultBranch,
		HooksMigrated:  migratedHooks,
		WIPBranch:      wipBranch,
		WIPWorktree:    wipWorktree,
		BranchesPushed: len(localBranches),
	}, nil
}

// resolveDefaultBranch returns the project's default branch, prompting the
// user (via opts.PromptDefaultBranch) only when it cannot be inferred.
func resolveDefaultBranch(name string, proj *config.Project, mainPath string, opts Options) (string, error) {
	if proj.DefaultBranch != "" {
		return proj.DefaultBranch, nil
	}
	if br := git.SymbolicRef(mainPath, "refs/remotes/origin/HEAD"); br != "" {
		// strip "origin/"
		if i := strings.Index(br, "/"); i >= 0 {
			br = br[i+1:]
		}
		return br, nil
	}
	// Try common candidates that actually exist locally.
	var candidates []string
	for _, c := range []string{"main", "master", "trunk"} {
		if git.HasBranch(mainPath, c) {
			candidates = append(candidates, c)
		}
	}
	if opts.PromptDefaultBranch == nil {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		return "", fmt.Errorf("cannot determine default branch for %s and no prompter configured", name)
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

func upstreamFor(repoPath, branch string) string {
	out, err := runGitOut(repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// listActiveHooks returns hook filenames in dir that are NOT *.sample and
// have at least one executable bit set. Returns nil, nil if dir is missing.
func listActiveHooks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".sample") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// copyHooks copies the named hook files from srcDir to dstDir, preserving
// the executable bit. Returns the names that were successfully copied.
func copyHooks(srcDir, dstDir string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	var copied []string
	for _, name := range names {
		if err := copyFilePreservingMode(filepath.Join(srcDir, name), filepath.Join(dstDir, name)); err != nil {
			return copied, fmt.Errorf("copy hook %s: %w", name, err)
		}
		copied = append(copied, name)
	}
	return copied, nil
}

func copyFilePreservingMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// rollbackBare removes a partially-created bare repo. Best-effort.
func rollbackBare(barePath string) {
	_ = os.RemoveAll(barePath)
}
