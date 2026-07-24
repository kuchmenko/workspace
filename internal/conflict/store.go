// Package conflict persists unresolved workspace synchronization conflicts.
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

type Kind string

const (
	KindTOMLMerge        Kind = "toml-merge"
	KindTOMLPushFailed   Kind = "toml-push-failed"
	KindMainDivergence   Kind = "main-divergence"
	KindNeedsMigration   Kind = "needs-migration"
	KindNeedsBootstrap   Kind = "needs-bootstrap"
	KindPathBlocked      Kind = "path-blocked"
	KindCloneFailed      Kind = "clone-failed"
	KindBranchDuplicate  Kind = "branch-duplicate"
	KindBranchOrphan     Kind = "branch-orphan"
	KindMirrorPushFailed Kind = "mirror-push-failed"
)

type Conflict struct {
	ID         string          `json:"id"`
	Workspace  string          `json:"workspace"`
	Project    string          `json:"project,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Kind       Kind            `json:"kind"`
	DetectedAt time.Time       `json:"detected_at"`
	Details    json.RawMessage `json:"details,omitempty"`
}

type Store struct {
	path string
}

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

func Open() (*Store, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

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

func (s *Store) List() ([]Conflict, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Conflicts, nil
}

func matchKey(c Conflict) string {
	return string(c.Kind) + "|" + c.Workspace + "|" + c.Project + "|" + c.Branch
}

func (s *Store) Record(c Conflict) (bool, error) {
	c = ensureRecordDefaults(c)
	f, err := s.load()
	if err != nil {
		return false, err
	}
	if i := findMatch(f.Conflicts, c); i >= 0 {
		refreshExisting(&f.Conflicts[i], c)
		return false, s.save(f)
	}
	f.Conflicts = append(f.Conflicts, c)
	return true, s.save(f)
}

func ensureRecordDefaults(c Conflict) Conflict {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.DetectedAt.IsZero() {
		c.DetectedAt = time.Now().UTC()
	}
	return c
}

func findMatch(xs []Conflict, c Conflict) int {
	target := matchKey(c)
	for i := range xs {
		if matchKey(xs[i]) == target {
			return i
		}
	}
	return -1
}

func refreshExisting(existing *Conflict, fresh Conflict) {
	existing.DetectedAt = fresh.DetectedAt
	if fresh.Details != nil {
		existing.Details = fresh.Details
	}
}

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
