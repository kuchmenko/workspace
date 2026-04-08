// Sidecar bridge for the bootstrap command. The generic file/lock/pid
// machinery lives in internal/sidecar; this file only defines the
// command-specific value type and a thin facade over it.
//
// While the sidecar exists with a live pid the daemon skips its tick for
// the workspace, preventing daemon/bootstrap races on git operations and
// half-bootstrap state being pushed upstream. See internal/sidecar for the
// shared mechanics.
package bootstrap

import (
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

// DoneEntry captures one project that finished cloning. The bootstrap
// commit step replays these into workspace.toml in a single atomic write
// at the end.
type DoneEntry struct {
	DefaultBranch string    `json:"default_branch"`
	ClonedAt      time.Time `json:"cloned_at"`
}

// Sidecar is a bootstrap-shaped view over a generic sidecar.Sidecar. It
// hides the json.RawMessage round-trip so callers see strongly-typed
// DoneEntry values.
type Sidecar struct {
	*sidecar.Sidecar
}

// New creates a fresh bootstrap sidecar bound to wsRoot, owned by the
// current process. Save() must be called before any work runs — the lock
// isn't real until the file exists on disk.
func New(wsRoot string) *Sidecar {
	return &Sidecar{Sidecar: sidecar.New(wsRoot, sidecar.KindBootstrap)}
}

// Load reads an existing bootstrap sidecar for wsRoot. Returns (nil, nil)
// if no sidecar exists, which is the common case.
func Load(wsRoot string) (*Sidecar, error) {
	sc, err := sidecar.Load(wsRoot, sidecar.KindBootstrap)
	if err != nil || sc == nil {
		return nil, err
	}
	return &Sidecar{Sidecar: sc}, nil
}

// Save writes the bootstrap sidecar atomically.
func Save(sc *Sidecar) error {
	if sc == nil {
		return nil
	}
	return sidecar.Save(sc.Sidecar)
}

// Delete removes the bootstrap sidecar for wsRoot.
func Delete(wsRoot string) error {
	return sidecar.Delete(wsRoot, sidecar.KindBootstrap)
}

// IsAlive reports whether the bootstrap recorded in sc is still running.
func IsAlive(sc *Sidecar) bool {
	if sc == nil {
		return false
	}
	return sidecar.IsAlive(sc.Sidecar)
}

// MarkDone records a successful clone for the named project.
func (s *Sidecar) MarkDone(name, defaultBranch string) error {
	return s.Set(name, DoneEntry{
		DefaultBranch: defaultBranch,
		ClonedAt:      time.Now().UTC(),
	})
}

// DoneEntries returns the per-project results recorded so far, decoded.
// Used at commit time to apply default_branch values back into
// workspace.toml in a single atomic write.
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
