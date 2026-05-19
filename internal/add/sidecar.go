package add

import (
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/sidecar"
)

type sidecarPayload struct {
	Mode Mode     `json:"mode"`
	URLs []string `json:"urls,omitempty"`
}

const sidecarPayloadKey = "__session__"

func acquireSidecar(wsRoot string, mode Mode, urls []string) (*sidecar.Sidecar, error) {
	existing, err := sidecar.Load(wsRoot, sidecar.KindAdd)
	if err != nil {
		return nil, fmt.Errorf("read add sidecar: %w", err)
	}
	if existing != nil {
		if sidecar.IsAlive(existing) {
			var pay sidecarPayload
			_, _ = existing.Get(sidecarPayloadKey, &pay)
			return nil, fmt.Errorf(
				"another `ws add` is running (pid %d, started %s, %s)",
				existing.Meta.PID,
				existing.Meta.Started.Local().Format(time.RFC3339),
				describePayload(pay),
			)
		}

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

func releaseSidecar(wsRoot string) {
	_ = sidecar.Delete(wsRoot, sidecar.KindAdd)
}

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
