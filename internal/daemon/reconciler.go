package daemon

import (
	"log"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

type Reconciler struct {
	root   string
	logger *log.Logger
	store  *conflict.Store

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
