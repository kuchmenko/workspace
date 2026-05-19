package migrate

import (
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

type DoneEntry struct {
	DefaultBranch string    `json:"default_branch"`
	MigratedAt    time.Time `json:"migrated_at"`
}

type Sidecar struct {
	*sidecar.Sidecar
}

func New(wsRoot string) *Sidecar {
	return &Sidecar{Sidecar: sidecar.New(wsRoot, sidecar.KindMigrate)}
}

func Load(wsRoot string) (*Sidecar, error) {
	sc, err := sidecar.Load(wsRoot, sidecar.KindMigrate)
	if err != nil || sc == nil {
		return nil, err
	}
	return &Sidecar{Sidecar: sc}, nil
}

func Save(sc *Sidecar) error {
	if sc == nil {
		return nil
	}
	return sidecar.Save(sc.Sidecar)
}

func Delete(wsRoot string) error {
	return sidecar.Delete(wsRoot, sidecar.KindMigrate)
}

func IsAlive(sc *Sidecar) bool {
	if sc == nil {
		return false
	}
	return sidecar.IsAlive(sc.Sidecar)
}

func (s *Sidecar) MarkDone(name, defaultBranch string) error {
	return s.Set(name, DoneEntry{
		DefaultBranch: defaultBranch,
		MigratedAt:    time.Now().UTC(),
	})
}

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
