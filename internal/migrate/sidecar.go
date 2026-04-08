// Sidecar bridge for the migrate command. The generic file/lock/pid
// machinery lives in internal/sidecar; this file only defines the
// command-specific value type and a thin facade over it.
//
// While the sidecar exists with a live pid the daemon skips its tick for
// the workspace, preventing daemon/migrate races on git operations and
// half-migrated state being pushed upstream. See internal/sidecar for the
// shared mechanics.
package migrate

import (
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

// DoneEntry captures one project that finished migrating. Recorded so a
// crashed mid-batch migrate can resume from where it left off without
// re-doing already-converted projects.
type DoneEntry struct {
	DefaultBranch string    `json:"default_branch"`
	MigratedAt    time.Time `json:"migrated_at"`
}

// Sidecar is a migrate-shaped view over a generic sidecar.Sidecar.
type Sidecar struct {
	*sidecar.Sidecar
}

// New creates a fresh migrate sidecar bound to wsRoot, owned by the
// current process. Save() must be called before any project is migrated —
// the lock isn't real until the file exists on disk.
func New(wsRoot string) *Sidecar {
	return &Sidecar{Sidecar: sidecar.New(wsRoot, sidecar.KindMigrate)}
}

// Load reads an existing migrate sidecar for wsRoot. Returns (nil, nil)
// if no sidecar exists, which is the common case.
func Load(wsRoot string) (*Sidecar, error) {
	sc, err := sidecar.Load(wsRoot, sidecar.KindMigrate)
	if err != nil || sc == nil {
		return nil, err
	}
	return &Sidecar{Sidecar: sc}, nil
}

// Save writes the migrate sidecar atomically.
func Save(sc *Sidecar) error {
	if sc == nil {
		return nil
	}
	return sidecar.Save(sc.Sidecar)
}

// Delete removes the migrate sidecar for wsRoot.
func Delete(wsRoot string) error {
	return sidecar.Delete(wsRoot, sidecar.KindMigrate)
}

// IsAlive reports whether the migrate recorded in sc is still running.
func IsAlive(sc *Sidecar) bool {
	if sc == nil {
		return false
	}
	return sidecar.IsAlive(sc.Sidecar)
}

// MarkDone records a successful migration of name with its resolved
// default_branch.
func (s *Sidecar) MarkDone(name, defaultBranch string) error {
	return s.Set(name, DoneEntry{
		DefaultBranch: defaultBranch,
		MigratedAt:    time.Now().UTC(),
	})
}

// DoneEntries returns the per-project results recorded so far, decoded.
func (s *Sidecar) DoneEntries() (map[string]DoneEntry, error) {
	out := make(map[string]DoneEntry, len(s.Done))
	for name := range s.Done {
		var entry DoneEntry
		if _, err := s.Get(name, &entry); err != nil {
			return nil, err
		}
		out[name] = entry
	}
	return out, nil
}
