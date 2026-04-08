// Package bootstrap owns the workspace bootstrap flow: scanning a fresh
// workspace.toml on a new machine, planning what needs to be cloned, and
// running the clones in a way that survives crashes and coordinates with the
// reconciler.
//
// Two pieces live here:
//
//   - Sidecar (this file) — a per-workspace progress file at
//     ~/.local/state/ws/bootstrap/<sha>.toml that doubles as a coordination
//     lock for the daemon. While the sidecar exists with a live pid, the
//     daemon skips both phases of its tick for that workspace, so we never
//     push half-bootstrapped workspace.toml upstream and never race the
//     reconciler on git operations.
//
//   - ScanPlan / DetectSelf (bootstrap.go) — pure logic the TUI calls into.
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

// Sidecar is the on-disk progress file for one in-flight bootstrap run.
//
// It is intentionally NOT stored inside the workspace git tree: we don't want
// it accidentally committed, and we don't want it to trigger fsnotify on
// workspace.toml itself.
type Sidecar struct {
	Meta SidecarMeta          `toml:"meta"`
	Done map[string]DoneEntry `toml:"done"`
}

// SidecarMeta records who created the sidecar and when. PID is checked for
// liveness so a crashed bootstrap run becomes detectably stale.
type SidecarMeta struct {
	PID           int       `toml:"pid"`
	Started       time.Time `toml:"started"`
	WorkspaceRoot string    `toml:"workspace_root"`
}

// DoneEntry captures one project that finished cloning. The bootstrap commit
// step replays these into workspace.toml in a single atomic write at the end.
type DoneEntry struct {
	DefaultBranch string    `toml:"default_branch"`
	ClonedAt      time.Time `toml:"cloned_at"`
}

// stateDir returns the directory containing all sidecar files.
// Honors $XDG_STATE_HOME, falls back to ~/.local/state/ws/bootstrap.
func stateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", "bootstrap"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", "bootstrap"), nil
}

// hashWorkspace produces a stable, filesystem-safe identifier for a workspace
// root. We hash so different absolute paths can never collide on filename.
func hashWorkspace(wsRoot string) string {
	abs, err := filepath.Abs(wsRoot)
	if err != nil {
		abs = wsRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16]
}

// Path returns the absolute filesystem location of the sidecar for wsRoot.
// Does not check whether the file exists.
func Path(wsRoot string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hashWorkspace(wsRoot)+".toml"), nil
}

// Load reads the sidecar for wsRoot. Returns (nil, nil) if no sidecar exists
// (the common case — most ticks have no bootstrap in progress).
func Load(wsRoot string) (*Sidecar, error) {
	p, err := Path(wsRoot)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sidecar %s: %w", p, err)
	}
	var sc Sidecar
	if len(data) == 0 {
		return &sc, nil
	}
	if err := toml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse sidecar %s: %w", p, err)
	}
	if sc.Done == nil {
		sc.Done = make(map[string]DoneEntry)
	}
	return &sc, nil
}

// Save writes the sidecar atomically (tmp file + rename). The Sidecar's
// Meta.WorkspaceRoot drives the filename, so callers don't pass wsRoot
// separately.
func Save(sc *Sidecar) error {
	if sc == nil {
		return errors.New("save nil sidecar")
	}
	if sc.Meta.WorkspaceRoot == "" {
		return errors.New("sidecar has empty WorkspaceRoot")
	}
	p, err := Path(sc.Meta.WorkspaceRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if sc.Done == nil {
		sc.Done = make(map[string]DoneEntry)
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), ".bootstrap-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // best-effort cleanup if rename never happens

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(sc); err != nil {
		tmp.Close()
		return fmt.Errorf("encode sidecar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpName, p, err)
	}
	return nil
}

// Delete removes the sidecar for wsRoot. No-op if it doesn't exist.
func Delete(wsRoot string) error {
	p, err := Path(wsRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove sidecar %s: %w", p, err)
	}
	return nil
}

// IsAlive reports whether the pid recorded in sc is still running. Used by:
//
//   - the daemon, to decide whether to skip its tick
//   - `ws bootstrap`, to detect a stale sidecar from a crashed run
//
// Sends signal 0 (no-op) which fails with ESRCH if the process is gone and
// EPERM if it exists but is owned by another user — both are valid signals
// that the recorded pid is occupied by *something*, but only ESRCH means
// "definitely not our crashed bootstrap". We treat EPERM as alive to be
// conservative; the realistic case (same uid, our own process) returns nil.
func IsAlive(sc *Sidecar) bool {
	if sc == nil || sc.Meta.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(sc.Meta.PID)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	// errno-based check for ESRCH ("no such process")
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.ESRCH {
			return false
		}
	}
	// EPERM or other unexpected error → conservatively say alive.
	return true
}

// New constructs a fresh sidecar bound to wsRoot, marked as owned by the
// current process. Caller is expected to Save() it before starting work.
func New(wsRoot string) *Sidecar {
	abs, _ := filepath.Abs(wsRoot)
	return &Sidecar{
		Meta: SidecarMeta{
			PID:           os.Getpid(),
			Started:       time.Now().UTC(),
			WorkspaceRoot: abs,
		},
		Done: make(map[string]DoneEntry),
	}
}
