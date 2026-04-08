// Reconciler is the unified replacement for the legacy Syncer + Poller pair.
//
// On every tick it:
//  1. Synchronizes workspace.toml with the workspace's git remote (commit
//     local edits, pull remote changes, surface conflicts).
//  2. Reloads workspace.toml if the pull changed it.
//  3. Walks every active project and brings its on-disk state in line with
//     the registry, never doing destructive operations inside project repos.
//
// The reconciler is intentionally idempotent: a missed tick or duplicate
// trigger never breaks state, because each tick computes the desired state
// from scratch and converges toward it.
package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"errors"

	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

// Reconciler manages one workspace.
type Reconciler struct {
	root   string
	logger *log.Logger
	store  *conflict.Store

	mu sync.Mutex // serialize Tick() invocations

	// Per-project exponential backoff. Keyed by project name.
	backoff map[string]*backoffState

	interval    time.Duration
	maxInterval time.Duration

	// autoBootstrap controls whether the daemon clones missing projects on
	// each tick. Default true; set false via daemon.toml to disable.
	autoBootstrap bool
}

type backoffState struct {
	nextAllowedAt time.Time
	currentDelay  time.Duration
}

// NewReconciler builds a Reconciler for the given workspace root.
// `interval` is the base poll interval; failed projects back off up to
// `maxInterval`.
func NewReconciler(root string, interval time.Duration, logger *log.Logger) *Reconciler {
	if interval < time.Minute {
		interval = 5 * time.Minute
	}
	store, err := conflict.Open()
	if err != nil {
		logger.Printf("reconciler: cannot open conflicts store: %v", err)
	}
	return &Reconciler{
		root:          root,
		logger:        logger,
		store:         store,
		backoff:       make(map[string]*backoffState),
		interval:      interval,
		maxInterval:   time.Hour,
		autoBootstrap: true,
	}
}

// SetAutoBootstrap toggles auto-cloning of missing projects. Wired from
// daemon.toml during workspace registration.
func (r *Reconciler) SetAutoBootstrap(v bool) {
	r.autoBootstrap = v
}

// Run starts the reconciler loop. It performs an immediate tick at startup
// (closing the "I just got back to my machine" gap) and then ticks on the
// configured interval until quit is closed.
func (r *Reconciler) Run(quit <-chan struct{}) {
	r.logger.Printf("reconciler: starting for %s (interval=%s)", r.root, r.interval)
	r.Tick()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-quit:
			return
		case <-ticker.C:
			r.Tick()
		}
	}
}

// Tick performs one full reconciliation pass. Safe to call concurrently;
// invocations are serialized.
func (r *Reconciler) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Interactive-command coordination: if any sidecar exists for this
	// workspace with a live pid (currently bootstrap or migrate), pause
	// both phases entirely. Sidecar existence + live pid is the lock;
	// daemon never writes to those files. Other workspaces in daemon.toml
	// have their own reconcilers and are unaffected (each has its own r.mu).
	if sc := sidecar.AnyActive(r.root); sc != nil {
		r.logger.Printf("reconciler: %s in progress for %s (pid %d), skipping tick", sc.Meta.Kind, r.root, sc.Meta.PID)
		return
	}

	// Phase 1: workspace.toml sync
	tomlChanged, err := r.syncTOML()
	if err != nil {
		r.logger.Printf("reconciler: toml sync error: %v", err)
	}

	// Phase 2: load (or reload) the workspace and reconcile projects.
	ws, err := config.Load(r.root)
	if err != nil {
		r.logger.Printf("reconciler: load workspace: %v", err)
		return
	}
	if tomlChanged {
		r.logger.Printf("reconciler: workspace.toml changed on disk, reloaded")
	}
	r.reconcileProjects(ws)
}

// =============================================================================
// Phase 1: workspace.toml sync
// =============================================================================

// syncTOML implements the decision matrix from the design proposal §6.2.
// Returns (tomlChangedOnDisk, error).
func (r *Reconciler) syncTOML() (bool, error) {
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlink: %w", err)
	}
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		return false, nil // not in a git repo, nothing to sync
	}
	if !git.HasRemote(repoRoot) {
		return false, nil
	}

	// Ensure the .gitattributes union-merge driver is in place. This makes
	// most concurrent edits to workspace.toml merge cleanly without manual
	// intervention. Best-effort: failure to write is logged but not fatal.
	if err := ensureUnionMerge(repoRoot, realPath); err != nil {
		r.logger.Printf("reconciler: ensureUnionMerge: %v", err)
	}

	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		return false, err
	}

	// Capture original HEAD so we can detect whether pull changed the file.
	originalHead := git.RevParse(repoRoot, "HEAD")

	if err := git.Fetch(repoRoot); err != nil {
		// Network failures here are common and not actionable; log and skip.
		r.logger.Printf("reconciler: fetch failed in %s: %v", repoRoot, err)
		return false, nil
	}

	localDirty := !isClean(repoRoot, relFile)
	branch, _ := git.CurrentBranch(repoRoot)
	if branch == "" {
		return false, fmt.Errorf("workspace repo is in detached HEAD")
	}
	ahead, behind, hasUpstream := git.AheadBehind(repoRoot, branch)
	if !hasUpstream {
		return false, nil
	}

	// Fast path: nothing to do.
	if !localDirty && ahead == 0 && behind == 0 {
		_ = r.clearTOMLConflicts()
		return false, nil
	}

	// Commit dirty changes first so the rest of the matrix only deals with
	// committed state.
	if localDirty {
		if err := git.Add(repoRoot, relFile); err != nil {
			return false, fmt.Errorf("git add: %w", err)
		}
		host := machineHostname()
		msg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", host)
		if err := git.Commit(repoRoot, msg); err != nil {
			return false, fmt.Errorf("git commit: %w", err)
		}
		ahead++
	}

	// Re-evaluate behind in case fetch happened pre-commit.
	_, behind, _ = git.AheadBehind(repoRoot, branch)

	// If remote moved while we were committing, rebase before push.
	if behind > 0 {
		if err := runIn(repoRoot, "git", "pull", "--rebase"); err != nil {
			r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, err)
			return false, err
		}
		_ = r.clearTOMLConflicts()
	}

	// Push if anything to push.
	if ahead > 0 || behind > 0 {
		if err := git.Push(repoRoot); err != nil {
			// One retry: fetch + rebase + push, mirror of the legacy syncer.
			if perr := runIn(repoRoot, "git", "pull", "--rebase"); perr != nil {
				r.recordTOMLConflict(repoRoot, conflict.KindTOMLMerge, perr)
				return false, perr
			}
			if perr := git.Push(repoRoot); perr != nil {
				r.recordTOMLConflict(repoRoot, conflict.KindTOMLPushFailed, perr)
				return false, perr
			}
		}
	}

	newHead := git.RevParse(repoRoot, "HEAD")
	return newHead != originalHead, nil
}

func (r *Reconciler) recordTOMLConflict(workspace string, kind conflict.Kind, cause error) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"error": cause.Error()})
	c := conflict.Conflict{
		Workspace: workspace,
		Kind:      kind,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("reconciler: record conflict: %v", err)
		return
	}
	if created {
		r.logger.Printf("reconciler: NEW conflict %s in %s: %v", kind, workspace, cause)
		conflict.NotifyNew(c)
	}
}

func (r *Reconciler) clearTOMLConflicts() error {
	if r.store == nil {
		return nil
	}
	for _, k := range []conflict.Kind{conflict.KindTOMLMerge, conflict.KindTOMLPushFailed} {
		_ = r.store.Clear(r.root, "", "", k)
	}
	return nil
}

// =============================================================================
// Phase 2: per-project reconcile
// =============================================================================

func (r *Reconciler) reconcileProjects(ws *config.Workspace) {
	machine := loadMachineName()
	now := time.Now()
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		if !proj.SyncEnabled() {
			r.logger.Printf("reconciler: %s auto_sync=false, fetch only", name)
		}
		if bs, ok := r.backoff[name]; ok && now.Before(bs.nextAllowedAt) {
			continue
		}
		if err := r.syncProject(name, proj, machine); err != nil {
			r.recordBackoff(name, err)
		} else {
			r.resetBackoff(name)
		}
	}
}

func (r *Reconciler) syncProject(name string, proj config.Project, machine string) error {
	mainPath := filepath.Join(r.root, proj.Path)
	barePath := layout.BarePath(mainPath)

	// Layout check: classify on-disk state and route accordingly.
	bareMissing := false
	mainMissing := false
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		bareMissing = true
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		mainMissing = true
	}

	if bareMissing && mainMissing {
		// Project is registered in workspace.toml but nothing exists on
		// disk. Auto-clone if enabled. Sequential semantics happen for
		// free: this clone is the only filesystem op for this project on
		// this tick, the next project's loop iteration runs after, and
		// the next tick reuses the now-present bare branch.
		if !r.autoBootstrap || !proj.SyncEnabled() {
			return nil
		}
		return r.autoCloneMissing(name, proj)
	}

	if bareMissing {
		// mainPath exists, no bare → plain checkout drift, needs migrate.
		r.recordProjectConflict(name, "", conflict.KindNeedsMigration, fmt.Sprintf("plain checkout at %s", mainPath))
		return nil
	}

	if err := git.Fetch(barePath); err != nil {
		return err // counts toward backoff
	}

	// auto_sync=false → fetch only, no push or pull.
	if !proj.SyncEnabled() {
		return nil
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return err
	}

	for _, wt := range wts {
		if wt.Bare || wt.Detached || wt.Branch == "" {
			continue
		}

		// "Main worktree" is strictly the one at proj.Path. We do NOT treat
		// any worktree on default_branch as main, because git allows --force
		// attaching another worktree to that branch and we don't want to
		// accidentally ff-pull a non-main checkout.
		isMain := wt.Path == mainPath

		// Skip anything where the user is mid-edit.
		if git.HasIndexLock(wt.Path) {
			continue
		}

		// Branches we own → push if ahead. Two ways to be "owned":
		//   1. wt/<this-machine>/* prefix (the default sync convention).
		//   2. Explicit opt-in via project.autopush.branches in
		//      workspace.toml, populated by `ws worktree new --auto-push`
		//      and `ws worktree promote`. Lets repository-native branch
		//      names (e.g. feat/fix-login) participate in auto-sync
		//      after they have been promoted out of the wt/* namespace.
		ownedByPrefix := machine != "" && strings.HasPrefix(wt.Branch, layout.BranchPrefix(machine))
		ownedByAutopush := proj.AutopushAllows(wt.Branch)
		if ownedByPrefix || ownedByAutopush {
			ahead, behind, has := git.AheadBehind(wt.Path, wt.Branch)
			if !has {
				// First push for this branch.
				if err := git.PushBranch(wt.Path, wt.Branch); err != nil {
					r.recordProjectConflict(name, wt.Branch, conflict.KindBranchDivergence, err.Error())
				}
				continue
			}
			if behind > 0 && ahead > 0 {
				r.recordProjectConflict(name, wt.Branch, conflict.KindBranchDivergence,
					fmt.Sprintf("ahead %d, behind %d — manual resolve needed", ahead, behind))
				continue
			}
			if ahead > 0 {
				if err := git.PushBranch(wt.Path, wt.Branch); err != nil {
					r.recordProjectConflict(name, wt.Branch, conflict.KindBranchDivergence, err.Error())
					continue
				}
				_ = r.clearProjectConflict(name, wt.Branch, conflict.KindBranchDivergence)
			}
			continue
		}

		// Main worktree on the project's default branch → ff-pull when safe.
		if isMain {
			if git.IsDirty(wt.Path) {
				continue
			}
			ahead, behind, has := git.AheadBehind(wt.Path, wt.Branch)
			if !has {
				continue
			}
			if behind > 0 && ahead == 0 {
				if err := git.Pull(wt.Path); err != nil {
					r.recordProjectConflict(name, wt.Branch, conflict.KindMainDivergence, err.Error())
					continue
				}
				_ = r.clearProjectConflict(name, wt.Branch, conflict.KindMainDivergence)
			} else if ahead > 0 && behind > 0 {
				r.recordProjectConflict(name, wt.Branch, conflict.KindMainDivergence,
					fmt.Sprintf("ahead %d, behind %d — main worktree should not be diverged", ahead, behind))
			}
			continue
		}

		// Other people's wt/<host>/* branches: nothing to do; the fetch
		// already updated their refs in bare.
	}
	return nil
}

// autoCloneMissing handles the "registered in workspace.toml but nothing on
// disk" case. Called from syncProject when both <path>.bare and <path> are
// absent and AutoBootstrap is enabled. Sequential by construction: one clone
// happens per project per tick, after which the project takes the existing-
// bare branch on subsequent ticks.
//
// Error mapping:
//   - ErrNeedsBootstrap → conflict 'needs-bootstrap' (default branch ambiguous)
//   - ErrPathBlocked    → conflict 'path-blocked'    (shouldn't really happen here, but defensive)
//   - any other error   → returned to caller, which feeds it into per-project
//     exponential backoff (network/auth flakes are the common case)
//
// On success, proj.DefaultBranch may have been filled in by CloneIntoLayout;
// we persist workspace.toml in place so the next tick (and the rest of the
// fleet via the workspace.toml sync) sees the new value.
func (r *Reconciler) autoCloneMissing(name string, proj config.Project) error {
	r.logger.Printf("reconciler: auto-clone %s from %s", name, proj.Remote)

	res, err := clone.CloneIntoLayout(r.root, name, &proj, clone.Options{
		Logf: r.logger.Printf,
		// Non-interactive: PromptDefaultBranch nil → ErrNeedsBootstrap if
		// the branch can't be auto-detected.
	})
	if err != nil {
		switch {
		case errors.Is(err, clone.ErrNeedsBootstrap):
			r.recordProjectConflict(name, "", conflict.KindNeedsBootstrap,
				"default branch could not be auto-detected — run `ws bootstrap "+name+"`")
			return nil
		case errors.Is(err, clone.ErrPathBlocked):
			r.recordProjectConflict(name, "", conflict.KindPathBlocked,
				"non-repo files at project path — clean up manually and re-run")
			return nil
		case errors.Is(err, clone.ErrNeedsMigration), errors.Is(err, clone.ErrAlreadyCloned):
			// Both indicate disk state changed under us between the stat
			// and the clone. Treat as a no-op; the next tick will route
			// the project through the normal sync path.
			return nil
		default:
			r.recordProjectConflict(name, "", conflict.KindCloneFailed, err.Error())
			return err
		}
	}

	r.logger.Printf("reconciler: cloned %s → %s (default_branch=%s)", name, res.BarePath, res.DefaultBranch)
	// Clear any previously recorded clone failure for this project.
	_ = r.clearProjectConflict(name, "", conflict.KindCloneFailed)
	_ = r.clearProjectConflict(name, "", conflict.KindNeedsBootstrap)

	// Persist default_branch back into workspace.toml. We re-load from disk
	// to avoid trampling unrelated edits the user (or another reconciler
	// for a different workspace) may have made between Phase 1 and now.
	if proj.DefaultBranch != "" {
		fresh, err := config.Load(r.root)
		if err != nil {
			r.logger.Printf("reconciler: reload workspace.toml after clone: %v", err)
			return nil
		}
		stored, ok := fresh.Projects[name]
		if !ok {
			return nil // project was removed from registry mid-tick; nothing to update
		}
		if stored.DefaultBranch == "" {
			stored.DefaultBranch = proj.DefaultBranch
			fresh.Projects[name] = stored
			if err := config.Save(r.root, fresh); err != nil {
				r.logger.Printf("reconciler: save workspace.toml after clone: %v", err)
			}
		}
	}
	return nil
}

func (r *Reconciler) recordProjectConflict(project, branch string, kind conflict.Kind, msg string) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"message": msg})
	c := conflict.Conflict{
		Workspace: r.root,
		Project:   project,
		Branch:    branch,
		Kind:      kind,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("reconciler: record %s: %v", kind, err)
		return
	}
	if created {
		r.logger.Printf("reconciler: NEW conflict %s for %s/%s: %s", kind, project, branch, msg)
		conflict.NotifyNew(c)
	}
}

func (r *Reconciler) clearProjectConflict(project, branch string, kind conflict.Kind) error {
	if r.store == nil {
		return nil
	}
	return r.store.Clear(r.root, project, branch, kind)
}

// =============================================================================
// Backoff
// =============================================================================

func (r *Reconciler) recordBackoff(name string, cause error) {
	bs, ok := r.backoff[name]
	if !ok {
		bs = &backoffState{currentDelay: r.interval}
		r.backoff[name] = bs
	} else {
		bs.currentDelay *= 2
		if bs.currentDelay > r.maxInterval {
			bs.currentDelay = r.maxInterval
		}
	}
	bs.nextAllowedAt = time.Now().Add(bs.currentDelay)
	r.logger.Printf("reconciler: %s failed (%v); next attempt in %s", name, cause, bs.currentDelay)
}

func (r *Reconciler) resetBackoff(name string) {
	delete(r.backoff, name)
}

// =============================================================================
// Helpers
// =============================================================================

// findGitRoot walks up from dir looking for the nearest git repo. Returns
// "" if no repo is found before reaching the filesystem root.
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

// ensureUnionMerge appends `<rel> merge=union` to .gitattributes in the
// repo root if it isn't already configured. Idempotent.
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
	_, err = f.WriteString(wantLine + "\n")
	return err
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
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
