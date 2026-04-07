// Package conflict tracks unresolved sync conflicts for the workspace daemon.
//
// Conflicts are persisted to ~/.local/state/ws/conflicts.json so the user can
// inspect them via `ws sync resolve`. The reconciler is the only writer; the
// resolve CLI is the only reader/mutator. There is no IPC between them — they
// coordinate via the file alone, with a best-effort O_EXCL lock.
//
// Phase 4 ships the recording side. Phase 5 adds the resolve TUI and
// notify-send wiring.
package conflict

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Kind enumerates the types of conflicts the reconciler can record.
type Kind string

const (
	KindTOMLMerge        Kind = "toml-merge"        // workspace.toml rebase failed
	KindTOMLPushFailed   Kind = "toml-push-failed"  // toml push rejected and pull-rebase did not help
	KindBranchDivergence Kind = "branch-divergence" // a wt/<machine>/* branch diverged from origin
	KindMainDivergence   Kind = "main-divergence"   // main worktree cannot fast-forward
	KindNeedsMigration   Kind = "needs-migration"   // project on disk is plain checkout, not yet migrated
)

// Conflict is one row in the persisted store.
type Conflict struct {
	ID         string          `json:"id"`
	Workspace  string          `json:"workspace"`
	Project    string          `json:"project,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Kind       Kind            `json:"kind"`
	DetectedAt time.Time       `json:"detected_at"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// Store is the on-disk JSON file. Concurrent writers within a single process
// are serialized via the embedded mutex; cross-process safety relies on the
// reconciler being the only writer.
type Store struct {
	path string
}

// Path returns the canonical conflicts.json location.
// Honors $XDG_STATE_HOME, falls back to ~/.local/state/ws/conflicts.json.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", "conflicts.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", "conflicts.json"), nil
}

// Open returns a Store backed by the canonical path.
func Open() (*Store, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

// fileShape is the JSON envelope.
type fileShape struct {
	Conflicts []Conflict `json:"conflicts"`
}

func (s *Store) load() (*fileShape, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &fileShape{}, nil
		}
		return nil, err
	}
	var f fileShape
	if len(data) == 0 {
		return &f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.path, err)
	}
	return &f, nil
}

func (s *Store) save(f *fileShape) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns all currently tracked conflicts.
func (s *Store) List() ([]Conflict, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Conflicts, nil
}

// matchKey identifies a conflict for deduplication purposes. Two records
// with the same key represent the same underlying problem and should not
// produce duplicate entries on every reconciler tick.
func matchKey(c Conflict) string {
	return string(c.Kind) + "|" + c.Workspace + "|" + c.Project + "|" + c.Branch
}

// Record inserts c if no equivalent conflict already exists, otherwise it
// refreshes the existing record's DetectedAt and Details. Returns true when
// a new conflict was inserted (so callers can decide whether to notify).
func (s *Store) Record(c Conflict) (bool, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.DetectedAt.IsZero() {
		c.DetectedAt = time.Now().UTC()
	}
	f, err := s.load()
	if err != nil {
		return false, err
	}
	key := matchKey(c)
	for i := range f.Conflicts {
		if matchKey(f.Conflicts[i]) == key {
			f.Conflicts[i].DetectedAt = c.DetectedAt
			if c.Details != nil {
				f.Conflicts[i].Details = c.Details
			}
			return false, s.save(f)
		}
	}
	f.Conflicts = append(f.Conflicts, c)
	return true, s.save(f)
}

// Clear removes any conflict matching workspace+project+branch+kind. Used
// when a tick proves the previously-recorded condition is now resolved
// (e.g. branch became ff again).
func (s *Store) Clear(workspace, project, branch string, kind Kind) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	target := matchKey(Conflict{Workspace: workspace, Project: project, Branch: branch, Kind: kind})
	out := f.Conflicts[:0]
	for _, c := range f.Conflicts {
		if matchKey(c) == target {
			continue
		}
		out = append(out, c)
	}
	f.Conflicts = out
	return s.save(f)
}

// Remove deletes a conflict by ID. Used by `ws sync resolve` after the user
// confirms a fix.
func (s *Store) Remove(id string) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	out := f.Conflicts[:0]
	for _, c := range f.Conflicts {
		if c.ID == id {
			continue
		}
		out = append(out, c)
	}
	f.Conflicts = out
	return s.save(f)
}
