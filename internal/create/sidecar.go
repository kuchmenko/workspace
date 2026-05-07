package create

import (
	"fmt"
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

// sidecarPayload describes the in-flight create operation. Stored
// inside the shared sidecar envelope so a second `ws create` running
// concurrently can tell the user what the first one is doing.
type sidecarPayload struct {
	Mode  Mode   `json:"mode"`
	Owner string `json:"owner,omitempty"`
	Name  string `json:"name,omitempty"`
}

// sidecarPayloadKey is the well-known entry name. The shared Done map
// is keyed by "project name"; ws create operates as a single session,
// so we use a fixed pseudo-entry.
const sidecarPayloadKey = "__session__"

// acquireSidecar persists the create sidecar for this Run. Refuses if
// another `ws create` is already live; silently clears stale records
// (dead pid) before acquiring. Mirrors the policy of `ws add` — we
// don't prompt for resume because there's no per-step recoverable
// state: a failed run either left no GitHub repo (gh create not yet
// called) or left a registered+cloned repo (which a re-run will
// detect via ErrAlreadyRegistered/ErrAlreadyCloned).
func acquireSidecar(wsRoot string, mode Mode, owner, name string) (*sidecar.Sidecar, error) {
	existing, err := sidecar.Load(wsRoot, sidecar.KindCreate)
	if err != nil {
		return nil, fmt.Errorf("read create sidecar: %w", err)
	}
	if existing != nil {
		if sidecar.IsAlive(existing) {
			var pay sidecarPayload
			_, _ = existing.Get(sidecarPayloadKey, &pay)
			return nil, fmt.Errorf(
				"another `ws create` is running (pid %d, started %s, %s)",
				existing.Meta.PID,
				existing.Meta.Started.Local().Format(time.RFC3339),
				describePayload(pay),
			)
		}
		if err := sidecar.Delete(wsRoot, sidecar.KindCreate); err != nil {
			return nil, fmt.Errorf("clear stale create sidecar: %w", err)
		}
	}

	sc := sidecar.New(wsRoot, sidecar.KindCreate)
	if err := sc.Set(sidecarPayloadKey, sidecarPayload{Mode: mode, Owner: owner, Name: name}); err != nil {
		return nil, fmt.Errorf("encode sidecar payload: %w", err)
	}
	if err := sidecar.Save(sc); err != nil {
		return nil, fmt.Errorf("save create sidecar: %w", err)
	}
	return sc, nil
}

// releaseSidecar removes the file. Best-effort.
func releaseSidecar(wsRoot string) {
	_ = sidecar.Delete(wsRoot, sidecar.KindCreate)
}

func describePayload(p sidecarPayload) string {
	modeName := "auto"
	switch p.Mode {
	case ModeHeadless:
		modeName = "headless"
	case ModeTUI:
		modeName = "tui"
	}
	if p.Owner != "" && p.Name != "" {
		return fmt.Sprintf("%s mode, creating %s/%s", modeName, p.Owner, p.Name)
	}
	return modeName + " mode"
}
