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

type Kind string

const (
	KindBootstrap Kind = "bootstrap"
	KindMigrate   Kind = "migrate"
	KindAdd       Kind = "add"
	KindCreate    Kind = "create"
)

type Meta struct {
	PID           int       `toml:"pid"`
	Started       time.Time `toml:"started"`
	WorkspaceRoot string    `toml:"workspace_root"`
	Kind          Kind      `toml:"kind"`
}

type Sidecar struct {
	Meta Meta                       `toml:"meta"`
	Done map[string]json.RawMessage `toml:"done"`
}

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

func (s *Sidecar) Has(name string) bool {
	_, ok := s.Done[name]
	return ok
}

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

func hashWorkspace(wsRoot string) string {
	abs, err := filepath.Abs(wsRoot)
	if err != nil {
		abs = wsRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16]
}

func Path(wsRoot string, kind Kind) (string, error) {
	dir, err := stateDir(kind)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hashWorkspace(wsRoot)+".toml"), nil
}

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

func Save(sc *Sidecar) error {
	if err := validateSidecarForSave(sc); err != nil {
		return err
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
	return atomicWriteSidecar(sc, p)
}

func validateSidecarForSave(sc *Sidecar) error {
	if sc == nil {
		return errors.New("save nil sidecar")
	}
	if sc.Meta.WorkspaceRoot == "" {
		return errors.New("sidecar has empty WorkspaceRoot")
	}
	if sc.Meta.Kind == "" {
		return errors.New("sidecar has empty Kind")
	}
	return nil
}

func atomicWriteSidecar(sc *Sidecar, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+string(sc.Meta.Kind)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := toml.NewEncoder(tmp).Encode(sc); err != nil {
		tmp.Close()
		return fmt.Errorf("encode sidecar: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpName, dest, err)
	}
	return nil
}

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

func IsAlive(sc *Sidecar) bool {
	if sc == nil || sc.Meta.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(sc.Meta.PID)
	if err != nil {
		return false
	}
	return interpretSignalError(proc.Signal(syscall.Signal(0)))
}

func interpretSignalError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.ESRCH {
		return false
	}
	return true
}

func AnyActive(wsRoot string) *Sidecar {
	for _, k := range []Kind{KindBootstrap, KindMigrate, KindAdd, KindCreate} {
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
