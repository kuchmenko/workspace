// Package conflict tracks unresolved sync conflicts for the workspace daemon.
//
// Conflicts are persisted to ~/.local/state/ws/conflicts.json so the user can
// inspect them via `ws sync resolve`. The reconciler is the only writer; the
// resolve CLI is the only reader/mutator. There is no IPC between them — they
// coordinate via the file alone, with a best-effort O_EXCL lock.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

func Notify(title, body string) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return
	}
	_ = exec.Command("notify-send", "-a", "ws", title, body).Run()
}

func NotifyNew(c Conflict) {
	title := fmt.Sprintf("ws: new sync conflict (%s)", c.Kind)
	var body string
	if c.Project != "" {
		body = fmt.Sprintf("%s/%s — run 'ws sync resolve'", c.Project, c.Branch)
	} else {
		body = "workspace.toml — run 'ws sync resolve'"
	}
	Notify(title, body)
}

type ConflictKind string

const (
	KindTOMLMerge       ConflictKind = "toml-merge"
	KindTOMLPushFailed  ConflictKind = "toml-push-failed"
	KindMainDivergence  ConflictKind = "main-divergence"
	KindNeedsMigration  ConflictKind = "needs-migration"
	KindNeedsBootstrap  ConflictKind = "needs-bootstrap"
	KindPathBlocked     ConflictKind = "path-blocked"
	KindCloneFailed     ConflictKind = "clone-failed"
	KindBranchDuplicate ConflictKind = "branch-duplicate"
	KindBranchOrphan    ConflictKind = "branch-orphan"

	// KindMirrorPushFailed reuses the Branch field for the mirror remote
	// name so the store dedupes per (project, mirror) pair.
	KindMirrorPushFailed ConflictKind = "mirror-push-failed"
)

type Conflict struct {
	ID         string          `json:"id"`
	Workspace  string          `json:"workspace"`
	Project    string          `json:"project,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Kind       ConflictKind    `json:"kind"`
	DetectedAt time.Time       `json:"detected_at"`
	Details    json.RawMessage `json:"details,omitempty"`
}

type ConflictStore struct {
	path string
}

func ConflictPath() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", "conflicts.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", "conflicts.json"), nil
}

func OpenConflictStore() (*ConflictStore, error) {
	p, err := ConflictPath()
	if err != nil {
		return nil, err
	}
	return &ConflictStore{path: p}, nil
}

type fileShape struct {
	Conflicts []Conflict `json:"conflicts"`
}

func (s *ConflictStore) load() (*fileShape, error) {
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

func (s *ConflictStore) save(f *fileShape) error {
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

func (s *ConflictStore) List() ([]Conflict, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Conflicts, nil
}

func matchKey(c Conflict) string {
	return string(c.Kind) + "|" + c.Workspace + "|" + c.Project + "|" + c.Branch
}

func (s *ConflictStore) Record(c Conflict) (bool, error) {
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

func (s *ConflictStore) Clear(workspace, project, branch string, kind ConflictKind) error {
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

func (s *ConflictStore) Remove(id string) error {
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
