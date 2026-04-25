// Package sidecar implements the per-workspace progress + lockfile pattern
// shared between long-running interactive commands like `ws bootstrap` and
// `ws migrate`.
//
// A sidecar is:
//
//   - A toml file at $XDG_STATE_HOME/ws/<kind>/<sha>.toml (default
//     ~/.local/state/ws/<kind>/<sha>.toml). The path is keyed by the
//     workspace root, so each workspace has its own sidecar per kind.
//
//   - A coordination lock for the daemon. While the file exists with a live
//     pid in its meta block, the reconciler skips its entire tick for that
//     workspace — both Phase 1 (workspace.toml git sync) and Phase 2
//     (project reconcile). This prevents the daemon from racing the
//     interactive command on git operations or pushing half-completed state
//     upstream.
//
//   - A crash-recovery hint. If the recorded pid is no longer alive, a new
//     run of the same command can prompt the user to resume or discard.
//
// The Done map carries command-specific per-project entries. Bootstrap and
// migrate use different value shapes, so the package stores them as
// json.RawMessage and lets each command unmarshal into its own struct.
//
// IMPORTANT: sidecars live OUTSIDE the workspace git tree on purpose. We
// don't want them committed by accident, and we don't want fsnotify on
// workspace.toml to fire on every save.
package sidecar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

// Kind identifies which command owns the sidecar. Used as a subdirectory
// under the state dir so different sidecars don't collide on filename.
type Kind string

const (
	KindBootstrap Kind = "bootstrap"
	KindMigrate   Kind = "migrate"
	KindAdd       Kind = "add"
)

// Meta is the common header every sidecar file carries. It records who
// created the sidecar and when, plus the workspace it belongs to so we can
// find it again from the wsRoot alone.
type Meta struct {
	PID           int       `toml:"pid"`
	Started       time.Time `toml:"started"`
	WorkspaceRoot string    `toml:"workspace_root"`
	Kind          Kind      `toml:"kind"`
}

// Sidecar is the on-disk envelope. Done holds command-specific per-project
// entries as raw bytes; callers (un)marshal their own value type via the
// Get/Set helpers below.
type Sidecar struct {
	Meta Meta                       `toml:"meta"`
	Done map[string]json.RawMessage `toml:"done"`
}

// New constructs a fresh sidecar bound to wsRoot, marked as owned by the
// current process. The caller is expected to Save() it before starting
// work — the lock isn't real until the file exists on disk.
func New(wsRoot string, kind Kind) *Sidecar {
	abs, _ := filepath.Abs(wsRoot)
	return &Sidecar{
		Meta: Meta{
			PID:           os.Getpid(),
			Started:       time.Now().UTC(),
			WorkspaceRoot: abs,
			Kind:          kind,
		},
		Done: make(map[string]json.RawMessage),
	}
}

// Set marshals v as JSON and stores it under the project name. Each command
// defines its own DoneEntry-type and round-trips it through Set/Get.
func (s *Sidecar) Set(name string, v interface{}) error {
	if s.Done == nil {
		s.Done = make(map[string]json.RawMessage)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal sidecar entry %s: %w", name, err)
	}
	s.Done[name] = raw
	return nil
}

// Get unmarshals the entry for name into v. Returns false if no such entry.
func (s *Sidecar) Get(name string, v interface{}) (bool, error) {
	raw, ok := s.Done[name]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("unmarshal sidecar entry %s: %w", name, err)
	}
	return true, nil
}

// Has reports whether name has a recorded entry.
func (s *Sidecar) Has(name string) bool {
	_, ok := s.Done[name]
	return ok
}

// stateDir returns the directory containing all sidecar files for kind.
// Honors $XDG_STATE_HOME, falls back to ~/.local/state/ws/<kind>.
func stateDir(kind Kind) (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", string(kind)), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", string(kind)), nil
}

// hashWorkspace produces a stable, filesystem-safe identifier for a
// workspace root. Two distinct absolute paths can never collide on filename.
func hashWorkspace(wsRoot string) string {
	abs, err := filepath.Abs(wsRoot)
	if err != nil {
		abs = wsRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16]
}

// Path returns the absolute filesystem location of the sidecar for
// (wsRoot, kind). Does not check whether the file exists.
func Path(wsRoot string, kind Kind) (string, error) {
	dir, err := stateDir(kind)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hashWorkspace(wsRoot)+".toml"), nil
}

// Load reads the sidecar for (wsRoot, kind). Returns (nil, nil) if no
// sidecar exists — the common case, since most ticks have no command in
// progress.
func Load(wsRoot string, kind Kind) (*Sidecar, error) {
	p, err := Path(wsRoot, kind)
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
		sc.Done = make(map[string]json.RawMessage)
	}
	return &sc, nil
}

// Save writes the sidecar atomically (tmp file + rename). Filename is
// derived from sc.Meta.WorkspaceRoot + sc.Meta.Kind, so the caller doesn't
// pass them again.
func Save(sc *Sidecar) error {
	if sc == nil {
		return errors.New("save nil sidecar")
	}
	if sc.Meta.WorkspaceRoot == "" {
		return errors.New("sidecar has empty WorkspaceRoot")
	}
	if sc.Meta.Kind == "" {
		return errors.New("sidecar has empty Kind")
	}
	p, err := Path(sc.Meta.WorkspaceRoot, sc.Meta.Kind)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if sc.Done == nil {
		sc.Done = make(map[string]json.RawMessage)
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), "."+string(sc.Meta.Kind)+"-*.tmp")
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

// Delete removes the sidecar for (wsRoot, kind). No-op if it doesn't exist.
func Delete(wsRoot string, kind Kind) error {
	p, err := Path(wsRoot, kind)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove sidecar %s: %w", p, err)
	}
	return nil
}

// IsAlive reports whether the pid recorded in sc is still running. Sends
// signal 0 (no-op) and inspects the result:
//
//   - nil error              → process exists, alive
//   - ESRCH                  → no such process, definitely dead
//   - os.ErrProcessDone      → already reaped
//   - any other (e.g. EPERM) → conservatively say alive (the pid is in use
//     by something, even if not by our crashed run, and we'd rather pause
//     the daemon for a tick than race)
func IsAlive(sc *Sidecar) bool {
	if sc == nil || sc.Meta.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(sc.Meta.PID)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return false
		}
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == syscall.ESRCH {
			return false
		}
		return true
	}
	return true
}

// AnyActive checks every known sidecar kind for wsRoot and reports the
// first one whose pid is alive. Used by the daemon's tick pre-check.
// Returns nil if no sidecars block this workspace.
func AnyActive(wsRoot string) *Sidecar {
	for _, k := range []Kind{KindBootstrap, KindMigrate, KindAdd} {
		sc, err := Load(wsRoot, k)
		if err != nil || sc == nil {
			continue
		}
		if IsAlive(sc) {
			return sc
		}
	}
	return nil
}
