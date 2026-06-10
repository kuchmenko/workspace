package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/sidecar"
)

type Reconciler struct {
	root   string
	logger *log.Logger
	store  *ConflictStore

	mu sync.Mutex

	backoff map[string]*backoffState

	interval    time.Duration
	maxInterval time.Duration

	autoBootstrap bool

	pushCooldown time.Duration
}

type backoffState struct {
	nextAllowedAt time.Time
	currentDelay  time.Duration
}

func NewReconciler(root string, interval time.Duration, logger *log.Logger) *Reconciler {
	if interval < time.Minute {
		interval = 5 * time.Minute
	}
	store, err := OpenConflictStore()
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

func (r *Reconciler) SetAutoBootstrap(v bool) {
	r.autoBootstrap = v
}

func (r *Reconciler) SetPushCooldown(d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.pushCooldown = d
}

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

func (r *Reconciler) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if sc := sidecar.AnyActive(r.root); sc != nil {
		r.logger.Printf("reconciler: %s in progress for %s (pid %d), skipping tick", sc.Meta.Kind, r.root, sc.Meta.PID)
		return
	}

	tomlChanged, err := r.syncTOML()
	if err != nil {
		r.logger.Printf("reconciler: toml sync error: %v", err)
	}

	ws, err := config.Load(r.root)
	if err != nil {
		r.logger.Printf("reconciler: load workspace: %v", err)
		return
	}
	if tomlChanged {
		r.logger.Printf("reconciler: workspace.toml changed on disk, reloaded")
	}
	r.recordValidationIssues(ws)
	r.reconcileProjects(ws)
}

func (r *Reconciler) reconcileProjects(ws *config.Workspace) {
	machine := loadMachineName()
	now := time.Now()
	dirty := false
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
		touched := false
		if err := r.syncProject(name, &proj, machine, &touched); err != nil {
			r.recordBackoff(name, err)
		} else {
			r.resetBackoff(name)
		}
		if touched {
			ws.Projects[name] = proj
			dirty = true
		}
	}
	if dirty {
		if err := config.Save(r.root, ws); err != nil {
			r.logger.Printf("reconciler: save workspace.toml after metadata refresh: %v", err)
		}
	}
}

func (r *Reconciler) syncProject(name string, proj *config.Project, machine string, touched *bool) error {
	mainPath := filepath.Join(r.root, proj.Path)
	barePath := layout.BarePath(mainPath)

	bareMissing := false
	mainMissing := false
	if _, err := os.Stat(barePath); os.IsNotExist(err) {
		bareMissing = true
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		mainMissing = true
	}

	if bareMissing && mainMissing {
		if !r.autoBootstrap || !proj.SyncEnabled() {
			return nil
		}
		return r.autoCloneMissing(name, *proj)
	}

	if bareMissing {
		r.recordProjectConflict(name, "", KindNeedsMigration, fmt.Sprintf("plain checkout at %s", mainPath))
		return nil
	}

	if !git.HasFetchRefspec(barePath) {
		if err := git.SetFetchRefspec(barePath); err != nil {
			r.logger.Printf("reconciler: %s: repair fetch refspec: %v", name, err)
		}
	}

	if err := git.Fetch(barePath); err != nil {
		return err
	}

	if !proj.SyncEnabled() {
		return nil
	}

	r.syncMirrors(name, proj, barePath)

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return err
	}

	for _, wt := range wts {
		if wt.Bare || wt.Detached || wt.Branch == "" {
			continue
		}

		isMain := wt.Path == mainPath

		if git.HasIndexLock(wt.Path) {
			continue
		}

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
					r.recordProjectConflict(name, wt.Branch, KindMainDivergence, err.Error())
					continue
				}
				_ = r.clearProjectConflict(name, wt.Branch, KindMainDivergence)
			} else if ahead > 0 && behind > 0 {
				r.recordProjectConflict(name, wt.Branch, KindMainDivergence,
					fmt.Sprintf("ahead %d, behind %d — main worktree should not be diverged", ahead, behind))
			}
			continue
		}

		if machine != "" && proj.LookupBranch(wt.Branch) != nil {
			ahead, _, has := git.AheadBehind(wt.Path, wt.Branch)
			if has && ahead > 0 {
				if proj.TouchActive(wt.Branch, machine, time.Now()) {
					*touched = true
				}
			}
		}
	}

	for _, b := range proj.Branches {
		if b.LastPushedAt == "" {
			_ = r.clearProjectConflict(name, b.Name, KindBranchOrphan)
			continue
		}
		if git.HasRemoteBranch(barePath, "origin", b.Name) {
			_ = r.clearProjectConflict(name, b.Name, KindBranchOrphan)
			continue
		}
		details := fmt.Sprintf("origin ref refs/remotes/origin/%s missing post-fetch (last pushed by %s at %s)",
			b.Name, b.LastPushedMachine, b.LastPushedAt)
		r.recordProjectConflict(name, b.Name, KindBranchOrphan, details)
	}

	return nil
}

// syncMirrors pushes origin's refs to every declared mirror remote. Mirror
// failures never propagate: a dead mirror must not back off the project or
// block worktree fast-forwards.
func (r *Reconciler) syncMirrors(name string, proj *config.Project, barePath string) {
	r.clearStaleMirrorConflicts(name, proj.Mirrors)
	for _, mirror := range slices.Sorted(maps.Keys(proj.Mirrors)) {
		url := proj.Mirrors[mirror]
		if err := git.EnsureMirrorRemote(barePath, mirror, url); err != nil {
			r.recordMirrorConflict(name, mirror, url, err)
			continue
		}
		if err := git.PushMirror(barePath, mirror); err != nil {
			r.recordMirrorConflict(name, mirror, url, err)
			continue
		}
		_ = r.clearProjectConflict(name, mirror, KindMirrorPushFailed)
	}
}

func (r *Reconciler) clearStaleMirrorConflicts(name string, mirrors map[string]string) {
	if r.store == nil {
		return
	}
	conflicts, err := r.store.List()
	if err != nil {
		return
	}
	for _, c := range conflicts {
		if c.Kind != KindMirrorPushFailed || c.Workspace != r.root || c.Project != name {
			continue
		}
		if _, still := mirrors[c.Branch]; !still {
			_ = r.store.Remove(c.ID)
		}
	}
}

func (r *Reconciler) recordMirrorConflict(project, mirror, url string, cause error) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{
		"message": cause.Error(),
		"mirror":  mirror,
		"url":     url,
	})
	c := Conflict{
		Workspace: r.root,
		Project:   project,
		Branch:    mirror,
		Kind:      KindMirrorPushFailed,
		Details:   details,
	}
	created, err := r.store.Record(c)
	if err != nil {
		r.logger.Printf("reconciler: record %s: %v", KindMirrorPushFailed, err)
		return
	}
	if created {
		r.logger.Printf("reconciler: NEW conflict %s for %s mirror %s: %v", KindMirrorPushFailed, project, mirror, cause)
		NotifyNew(c)
	}
}

func (r *Reconciler) autoCloneMissing(name string, proj config.Project) error {
	r.logger.Printf("reconciler: auto-clone %s from %s", name, proj.Remote)

	res, err := git.CloneIntoLayout(r.root, name, &proj, git.CloneOptions{
		Logf: r.logger.Printf,
	})
	if err != nil {
		switch {
		case errors.Is(err, git.ErrNeedsBootstrap):
			r.recordProjectConflict(name, "", KindNeedsBootstrap,
				"default branch could not be auto-detected — run `ws bootstrap "+name+"`")
			return nil
		case errors.Is(err, git.ErrPathBlocked):
			r.recordProjectConflict(name, "", KindPathBlocked,
				"non-repo files at project path — clean up manually and re-run")
			return nil
		case errors.Is(err, git.ErrNeedsMigration), errors.Is(err, git.ErrAlreadyCloned):

			return nil
		default:
			r.recordProjectConflict(name, "", KindCloneFailed, err.Error())
			return err
		}
	}

	r.logger.Printf("reconciler: cloned %s → %s (default_branch=%s)", name, res.BarePath, res.DefaultBranch)

	_ = r.clearProjectConflict(name, "", KindCloneFailed)
	_ = r.clearProjectConflict(name, "", KindNeedsBootstrap)

	if proj.DefaultBranch != "" {
		fresh, err := config.Load(r.root)
		if err != nil {
			r.logger.Printf("reconciler: reload workspace.toml after clone: %v", err)
			return nil
		}
		stored, ok := fresh.Projects[name]
		if !ok {
			return nil
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

func (r *Reconciler) recordValidationIssues(ws *config.Workspace) {
	for _, issue := range ws.Validate() {
		switch issue.Kind {
		case config.ValidationDuplicateBranch:
			r.recordProjectConflict(issue.Project, issue.Branch, KindBranchDuplicate, issue.Detail)
		}
	}
}

func (r *Reconciler) recordProjectConflict(project, branch string, kind ConflictKind, msg string) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"message": msg})
	c := Conflict{
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
		NotifyNew(c)
	}
}

func (r *Reconciler) clearProjectConflict(project, branch string, kind ConflictKind) error {
	if r.store == nil {
		return nil
	}
	return r.store.Clear(r.root, project, branch, kind)
}

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

func (r *Reconciler) syncTOML() (bool, error) {
	tomlPath := filepath.Join(r.root, "workspace.toml")
	realPath, err := filepath.EvalSymlinks(tomlPath)
	if err != nil {
		return false, fmt.Errorf("resolve symlink: %w", err)
	}
	repoRoot := findGitRoot(filepath.Dir(realPath))
	if repoRoot == "" {
		return false, nil
	}
	if !git.HasRemote(repoRoot) {
		return false, nil
	}

	if err := ensureUnionMerge(repoRoot, realPath); err != nil {
		r.logger.Printf("reconciler: ensureUnionMerge: %v", err)
	}

	relFile, err := filepath.Rel(repoRoot, realPath)
	if err != nil {
		return false, err
	}

	originalHead := git.RevParse(repoRoot, "HEAD")

	if err := git.Fetch(repoRoot); err != nil {
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

	if !localDirty && ahead == 0 && behind == 0 {
		_ = r.clearTOMLConflicts()
		return false, nil
	}

	autoSyncMsg := fmt.Sprintf("ws: auto-sync workspace.toml from %s", machineHostname())
	if localDirty {
		if err := git.Add(repoRoot, relFile); err != nil {
			return false, fmt.Errorf("git add: %w", err)
		}
		headMsg, _ := git.LastCommitMessage(repoRoot)
		if ahead > 0 && headMsg == autoSyncMsg {
			if err := runIn(repoRoot, "git", "diff", "--cached", "--quiet", "HEAD~1"); err == nil {
				if err := runIn(repoRoot, "git", "reset", "--mixed", "HEAD~1"); err != nil {
					return false, fmt.Errorf("drop empty held auto-sync: %w", err)
				}
				ahead--
			} else if err := runIn(repoRoot, "git", "commit", "--amend", "--no-edit"); err != nil {
				return false, fmt.Errorf("git commit --amend: %w", err)
			}
		} else {
			if err := git.Commit(repoRoot, autoSyncMsg); err != nil {
				return false, fmt.Errorf("git commit: %w", err)
			}
			ahead++
		}
	}

	_, behind, _ = git.AheadBehind(repoRoot, branch)

	if behind > 0 {
		if err := runIn(repoRoot, "git", "pull", "--rebase"); err != nil {
			r.recordTOMLConflict(repoRoot, KindTOMLMerge, err)
			return false, err
		}
		_ = r.clearTOMLConflicts()
	}

	if ahead > 0 || behind > 0 {
		if err := r.validateWorkspaceTOMLForPush(); err != nil {
			r.recordTOMLConflict(repoRoot, KindTOMLMerge, err)
			return false, err
		}
		if r.shouldHoldPush(repoRoot, autoSyncMsg, ahead) {
			r.logger.Printf("reconciler: %s holding auto-sync commit for amend (cooldown %s)", repoRoot, r.pushCooldown)
		} else if err := git.Push(repoRoot); err != nil {
			if perr := runIn(repoRoot, "git", "pull", "--rebase"); perr != nil {
				r.recordTOMLConflict(repoRoot, KindTOMLMerge, perr)
				return false, perr
			}
			if perr := git.Push(repoRoot); perr != nil {
				r.recordTOMLConflict(repoRoot, KindTOMLPushFailed, perr)
				return false, perr
			}
		}
	}

	newHead := git.RevParse(repoRoot, "HEAD")
	return newHead != originalHead, nil
}

func (r *Reconciler) validateWorkspaceTOMLForPush() error {
	if _, err := config.Load(r.root); err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) shouldHoldPush(repoRoot, autoSyncMsg string, ahead int) bool {
	if r.pushCooldown <= 0 {
		return false
	}
	if ahead != 1 {
		return false
	}
	headMsg, _ := git.LastCommitMessage(repoRoot)
	if headMsg != autoSyncMsg {
		return false
	}
	t, err := git.LastCommitAuthorTime(repoRoot)
	if err != nil {
		return false
	}
	return time.Since(t) < r.pushCooldown
}

func (r *Reconciler) recordTOMLConflict(workspace string, kind ConflictKind, cause error) {
	if r.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"error": cause.Error()})
	c := Conflict{
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
		NotifyNew(c)
	}
}

func (r *Reconciler) clearTOMLConflicts() error {
	if r.store == nil {
		return nil
	}
	for _, k := range []ConflictKind{KindTOMLMerge, KindTOMLPushFailed} {
		_ = r.store.Clear(r.root, "", "", k)
	}
	return nil
}
