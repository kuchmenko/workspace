package add

import (
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

// sidecarPayload is the `ws add` command-specific body stored inside
// the shared sidecar envelope. It records enough to tell a user who
// invokes `ws add` while another run is in flight what the other run
// is doing — mode + URLs it's processing.
//
// The shared sidecar struct round-trips arbitrary payload types via
// json.RawMessage; we slot this through sidecar.Set under a fixed key.
type sidecarPayload struct {
	Mode Mode     `json:"mode"`
	URLs []string `json:"urls,omitempty"`
}

// sidecarPayloadKey is the well-known entry name under which we store
// the payload. The shared Done map is keyed by "project name"; for the
// `add` kind we use a single pseudo-entry because Run operates as a
// session, not per-project.
const sidecarPayloadKey = "__session__"

// acquireSidecar creates and persists the sidecar for this Run. If a
// live sidecar already exists, returns an error describing the other
// run. If a stale sidecar exists, deletes it before acquiring.
//
// Phase 1 does NOT prompt for stale-sidecar resume (as bootstrap does)
// because `ws add` has no per-project mid-progress state to resume —
// a failed run's partial clone is either recoverable via a re-run
// (clone.ErrAlreadyCloned on the bare path) or leaves nothing behind
// (if the sidecar was written before any clone started).
func acquireSidecar(wsRoot string, mode Mode, urls []string) (*sidecar.Sidecar, error) {
	existing, err := sidecar.Load(wsRoot, sidecar.KindAdd)
	if err != nil {
		return nil, fmt.Errorf("read add sidecar: %w", err)
	}
	if existing != nil {
		if sidecar.IsAlive(existing) {
			// Describe the other run so the user can decide.
			var pay sidecarPayload
			_, _ = existing.Get(sidecarPayloadKey, &pay)
			return nil, fmt.Errorf(
				"another `ws add` is running (pid %d, started %s, %s)",
				existing.Meta.PID,
				existing.Meta.Started.Local().Format(time.RFC3339),
				describePayload(pay),
			)
		}
		// Stale: dead pid. Clear it silently — the failed run has no
		// recoverable state that a user could resume.
		if err := sidecar.Delete(wsRoot, sidecar.KindAdd); err != nil {
			return nil, fmt.Errorf("clear stale add sidecar: %w", err)
		}
	}

	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	if err := sc.Set(sidecarPayloadKey, sidecarPayload{Mode: mode, URLs: urls}); err != nil {
		return nil, fmt.Errorf("encode sidecar payload: %w", err)
	}
	if err := sidecar.Save(sc); err != nil {
		return nil, fmt.Errorf("save add sidecar: %w", err)
	}
	return sc, nil
}

// releaseSidecar removes the sidecar file. Best-effort — errors here
// mean "leftover sidecar on disk" which the next `ws add` invocation
// will recognize as stale and clear. Callers `defer` this on every
// exit path of Run.
func releaseSidecar(wsRoot string) {
	_ = sidecar.Delete(wsRoot, sidecar.KindAdd)
}

// describePayload summarizes the payload for "already running" error
// messages. Kept short to fit on one terminal line.
func describePayload(p sidecarPayload) string {
	modeName := "auto"
	switch p.Mode {
	case ModeHeadless:
		modeName = "headless"
	case ModeTUI:
		modeName = "tui"
	case ModeEmbedded:
		modeName = "embedded"
	}
	if len(p.URLs) == 0 {
		return modeName + " mode"
	}
	if len(p.URLs) == 1 {
		return fmt.Sprintf("%s mode, adding %s", modeName, p.URLs[0])
	}
	return fmt.Sprintf("%s mode, adding %d URLs: %s",
		modeName, len(p.URLs), strings.Join(p.URLs, ", "))
}
