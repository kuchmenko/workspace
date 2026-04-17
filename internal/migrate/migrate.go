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
	// StashBranch: when true, stash entries are converted into
	// wt/<machine>/migration-stash-<ts>-N branches via `git stash branch`
	// before the bare clone, so they survive into the new layout. Without
	// this flag, the presence of any stash entries aborts the migration
	// (stash refs are not copied by `clone --bare`).
	StashBranch bool
	// CheckoutDefault: when true, a project that is in detached HEAD has
	// its current commit preserved into a wt/<machine>/migration-detached-<ts>
	// branch (only if it isn't already reachable from a local branch),
	// then the working copy switches to default_branch and migration
	// proceeds. Without this flag, detached HEAD aborts the migration.
	CheckoutDefault bool
	// Machine is the sanitized machine name for branch namespacing. Required
	// when WIP, StashBranch, or CheckoutDefault is true.
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
	Project          string
	BarePath         string
	MainWorktree     string
	DefaultBranch    string
	HooksMigrated    []string
	WIPBranch        string   // non-empty when --wip created a snapshot branch
	WIPWorktree      string   // non-empty when --wip created an extra worktree
	StashBranches    []string // wt/<machine>/migration-stash-* branches created from stashes
	DetachedBranch   string   // wt/<machine>/migration-detached-* preserving orphaned commits
	BranchesPushed   int      // count of local branches preserved into bare
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

	// Step 3: pre-flight — handle detached HEAD, stash, dirty in this order.
	// Order matters: detached → checkout → stash → dirty. Each conversion
	// creates a side branch that becomes part of the bare clone.
	ts := time.Now().Unix()

	originalBranch, _ := git.CurrentBranch(mainPath)
	detachedBranch := ""
	if originalBranch == "" {
		// Detached HEAD. Either preserve and check out default_branch, or abort.
		if !opts.CheckoutDefault {
			return nil, fmt.Errorf("%s is in detached HEAD; check out a branch first or re-run with the interactive TUI", name)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("detached-HEAD recovery requires a configured machine name")
		}
		head := git.RevParse(mainPath, "HEAD")
		// If the current commit is reachable from any local branch, we can
		// safely walk away from it. Otherwise, snapshot it onto a side
		// branch so the bare clone picks it up.
		reachable, _ := commitReachableFromAnyBranch(mainPath, head)
		if !reachable {
			topic := fmt.Sprintf("migration-detached-%d", ts)
			detachedBranch = layout.BranchName(opts.Machine, topic)
			opts.logf("migrate %s: detached HEAD at %s → preserving as %s", name, head, detachedBranch)
			if err := runGit(mainPath, "branch", detachedBranch); err != nil {
				return nil, fmt.Errorf("preserve detached HEAD as branch %s: %w", detachedBranch, err)
			}
		} else {
			opts.logf("migrate %s: detached HEAD at %s — already reachable from a branch, no preservation needed", name, head)
		}
		opts.logf("migrate %s: checking out %s", name, defaultBranch)
		if err := runGit(mainPath, "checkout", defaultBranch); err != nil {
			return nil, fmt.Errorf("checkout %s from detached HEAD: %w", defaultBranch, err)
		}
		originalBranch = defaultBranch
	}

	// Stash entries: convert to side branches via `git stash branch`, or abort.
	stashCount := git.StashCount(mainPath)
	stashBranches := []string{}
	if stashCount > 0 {
		if !opts.StashBranch {
			return nil, fmt.Errorf("%s has %d stash entries; re-run with stash-branch enabled (TUI: pick `branch`) to convert each into a wt/<machine>/migration-stash-<ts>-N branch", name, stashCount)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("stash-to-branch requires a configured machine name")
		}
		// `git stash branch <name>` always pops the most recent (stash@{0}),
		// so we walk N times. Each call leaves us on the new branch with the
		// stash applied — we commit it and then checkout originalBranch again.
		for i := 0; i < stashCount; i++ {
			topic := fmt.Sprintf("migration-stash-%d-%d", ts, i)
			br := layout.BranchName(opts.Machine, topic)
			opts.logf("migrate %s: converting stash@{0} → %s", name, br)
			if err := runGit(mainPath, "stash", "branch", br); err != nil {
				return nil, fmt.Errorf("stash branch %s: %w", br, err)
			}
			// stash branch leaves the worktree dirty with the popped index
			// staged. Commit it so the branch carries real history rather
			// than just a checkout.
			if err := runGit(mainPath, "add", "-A"); err != nil {
				return nil, fmt.Errorf("stage stash branch %s: %w", br, err)
			}
			if err := runGit(mainPath, "commit", "-m", fmt.Sprintf("ws: migration stash@{0} snapshot (%d)", i)); err != nil {
				return nil, fmt.Errorf("commit stash branch %s: %w", br, err)
			}
			stashBranches = append(stashBranches, br)
			// Return to original branch before processing the next stash.
			if err := runGit(mainPath, "checkout", originalBranch); err != nil {
				return nil, fmt.Errorf("restore %s after stash branch: %w", originalBranch, err)
			}
		}
	}

	// Dirty working tree: snapshot to WIP branch or abort.
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
		wipTopic = fmt.Sprintf("migration-wip-%d", ts)
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
	// to mainPath, which is about to disappear), install the standard
	// fetch refspec (clone --bare omits it — without it fetch only updates
	// FETCH_HEAD and branch@{u} can never resolve), then fetch so
	// refs/remotes/origin/* is populated from the real remote.
	if proj.Remote != "" {
		if err := git.SetRemoteURL(barePath, proj.Remote); err != nil {
			rollbackBare(barePath)
			return nil, err
		}
		if err := git.SetFetchRefspec(barePath); err != nil {
			rollbackBare(barePath)
			return nil, fmt.Errorf("set fetch refspec: %w", err)
		}
		if err := git.Fetch(barePath); err != nil {
			// Network failure here is recoverable — we still have a valid
			// bare with all local objects. Just log and continue.
			opts.logf("migrate %s: warning: initial fetch failed: %v", name, err)
		}
		// best-effort: pin origin/HEAD to default_branch
		_ = git.SetRemoteHead(barePath, defaultBranch)
	}

	// Step 9: upstream tracking for the default branch so plain `git push`
	// and `git pull` work in the main worktree without arguments.
	// SetBranchUpstream writes branch.<default>.{remote,merge} via
	// `git config`, which works even if the Step-8 fetch failed (offline
	// migration) or hasn't populated refs/remotes/origin/<default> yet.
	// We don't restore upstream for every local branch — the reconciler
	// only pushes wt/<machine>/* and ordinary `git pull` resolves upstream
	// lazily. Best-effort: a failure here doesn't abort migration.
	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("migrate %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}

	// Step 10: migrate hooks
	migratedHooks, err := copyHooks(hooksDir, filepath.Join(barePath, "hooks"), activeHooks)
	if err != nil {
		opts.logf("migrate %s: warning: hook migration partial: %v", name, err)
	}

	// Step 11: replace the working dir's .git with a worktree pointer.
	//
	// `git worktree add --force <existing-non-empty-dir> <branch>` does NOT
	// work — modern git refuses to attach a worktree to a directory that
	// already has files, regardless of --force. (--force only relaxes the
	// "branch already checked out elsewhere" and "registered worktree
	// missing" checks.)
	//
	// Working strategy:
	//   1. Move existing .git aside to .git.migrating-<ts> (recoverable).
	//   2. Create the worktree at a sibling tmp path under a hidden parent
	//      dir, with --no-checkout. The worktree's basename matches
	//      mainPath's basename so git's admin dir at <bare>/worktrees/<name>
	//      gets a clean name (not "<name>.wt-tmp" or similar). git
	//      materializes the .git pointer file there but writes no
	//      working-tree files (and, importantly for step 6, populates no
	//      index entries either — see below).
	//   3. Move that .git pointer file from the tmp dir into mainPath
	//      (which still contains the user's untouched files).
	//   4. Remove the now-empty tmp dir AND its hidden parent.
	//   5. `git worktree repair` so the bare repo's worktrees/<name>/gitdir
	//      points at mainPath instead of the tmp location.
	//   6. `git reset --mixed HEAD` to populate the index from HEAD.
	//      `--no-checkout` leaves the index EMPTY (it only sets up the
	//      worktree's HEAD pointer). Without this step `git status` shows
	//      every file as both "deleted in index" and "untracked", which
	//      is technically a working repo but completely broken UX.
	//   7. Verify HEAD didn't shift and the worktree is clean.
	//
	// On any failure between steps 2–6 we restore the original .git from
	// .git.migrating-<ts> and tear down the bare. Step 7 is the last point
	// where a rollback is feasible.
	movedGit := filepath.Join(mainPath, fmt.Sprintf(".git.migrating-%d", ts))
	if err := os.Rename(filepath.Join(mainPath, ".git"), movedGit); err != nil {
		rollbackBare(barePath)
		return nil, fmt.Errorf("move .git aside: %w", err)
	}

	// Helper closure: rollback the .git move and clean up the bare. Used
	// from every failure branch below.
	restore := func() {
		_ = os.Rename(movedGit, filepath.Join(mainPath, ".git"))
		rollbackBare(barePath)
	}

	// Sibling hidden parent dir. The tmp worktree lives inside it with the
	// SAME basename as mainPath, so git's admin dir at
	// <bare>/worktrees/<basename> gets a sensible name. We rm -rf the
	// parent at the end, so the user never sees this dir.
	tmpParent := filepath.Join(filepath.Dir(mainPath), fmt.Sprintf(".ws-migrate-%d", ts))
	tmpWT := filepath.Join(tmpParent, filepath.Base(mainPath))
	// Defensive: stale dir from a previous crashed run.
	_ = os.RemoveAll(tmpParent)
	if err := os.MkdirAll(tmpParent, 0o755); err != nil {
		restore()
		return nil, fmt.Errorf("create tmp parent: %w", err)
	}

	if err := git.WorktreeAddNoCheckout(barePath, tmpWT, currentBranch); err != nil {
		_ = os.RemoveAll(tmpParent)
		restore()
		return nil, fmt.Errorf("create tmp worktree: %w", err)
	}

	// Move the .git pointer file (a regular file containing `gitdir: ...`,
	// not a directory) from the tmp dir into mainPath. The user's working
	// tree files in mainPath are untouched.
	tmpDotGit := filepath.Join(tmpWT, ".git")
	if err := os.Rename(tmpDotGit, filepath.Join(mainPath, ".git")); err != nil {
		_ = os.RemoveAll(tmpParent)
		restore()
		return nil, fmt.Errorf("move .git pointer from tmp: %w", err)
	}

	// Tmp parent should be empty (--no-checkout wrote nothing else).
	if err := os.RemoveAll(tmpParent); err != nil {
		opts.logf("migrate %s: warning: could not remove %s: %v", name, tmpParent, err)
	}

	// Tell git to update the worktrees admin dir so gitdir → mainPath, not
	// the now-removed tmp path.
	if err := git.WorktreeRepair(mainPath); err != nil {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("worktree repair: %w", err)
	}

	// Populate the index from HEAD. `git worktree add --no-checkout`
	// creates the worktree with HEAD set but the index EMPTY — without
	// this `git status` would show every tracked file as both
	// "deleted in index" and "untracked on disk". `reset --mixed HEAD`
	// reads HEAD into the index without touching the working tree, which
	// is exactly what we need: the existing files match HEAD, so after
	// the index is populated, status reports clean.
	if err := runGit(mainPath, "reset", "--mixed", "HEAD"); err != nil {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("populate index from HEAD: %w", err)
	}

	// Verify the new worktree is functional and HEAD didn't shift.
	if !git.IsRepo(mainPath) {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("worktree verification failed: %s is no longer a git repo", mainPath)
	}
	if newHead := git.RevParse(mainPath, "HEAD"); newHead != originalHead {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
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
		StashBranches:  stashBranches,
		DetachedBranch: detachedBranch,
		BranchesPushed: len(localBranches),
	}, nil
}

// commitReachableFromAnyBranch reports whether commit `sha` is an ancestor
// of any local branch in repoPath. Used by detached-HEAD recovery to decide
// whether the current commit needs to be preserved on a side branch before
// we walk away from it.
func commitReachableFromAnyBranch(repoPath, sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	branches, err := git.Branches(repoPath)
	if err != nil {
		return false, err
	}
	for _, b := range branches {
		if err := runGit(repoPath, "merge-base", "--is-ancestor", sha, b); err == nil {
			return true, nil
		}
	}
	return false, nil
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
