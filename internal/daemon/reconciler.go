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
	"log"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
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

	// pushCooldown coalesces consecutive auto-sync commits of workspace.toml
	// into a single squashed commit. While the most recent local commit is
	// our own auto-sync and younger than this duration, syncTOML amends
	// further dirty changes into it and defers the push. Zero disables
	// coalescing (push immediately after each commit) — that's the safe
	// default for `ws sync`, while the daemon opts in via SetPushCooldown.
	pushCooldown time.Duration
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

// SetPushCooldown configures how long a local auto-sync commit may be amended
// before it must be pushed. Zero disables amend+defer (push every commit
// immediately). Negative values are clamped to zero.
func (r *Reconciler) SetPushCooldown(d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.pushCooldown = d
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
	r.recordValidationIssues(ws)
	r.reconcileProjects(ws)
}
