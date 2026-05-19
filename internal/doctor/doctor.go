// Package doctor composes existing workspace primitives into a unified
// health check. It never performs git operations beyond what is needed to
// answer a check question, and never mutates on-disk state unless a caller
// explicitly runs a Fix attached to a Finding.
//
// The Runner collects Findings from a fixed catalog of checks:
//
//   - System checks (daemon, stale sidecars, active conflicts, config) run
//     once per invocation.
//   - Project checks (layout, refspec, remote, branch, index) run once per
//     active project.
//
// Checks are intentionally flat functions (not plugins) — the catalog is
// small and stable, and keeping the wiring explicit makes the order of
// evaluation obvious at a glance.
package doctor

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
)

type Severity int

const (
	OK Severity = iota

	Info

	Warn

	Error
)

func (s Severity) String() string {
	switch s {
	case OK:
		return "ok"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}

func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *Severity) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch raw {
	case "ok":
		*s = OK
	case "info":
		*s = Info
	case "warn":
		*s = Warn
	case "error":
		*s = Error
	default:
		return fmt.Errorf("doctor: unknown severity %q", raw)
	}
	return nil
}

type Finding struct {
	Scope string `json:"scope"`

	Check string `json:"check"`

	Severity Severity `json:"severity"`

	Message string `json:"message"`

	FixHint string `json:"fix_hint,omitempty"`

	Fixed bool `json:"fixed,omitempty"`

	FixError string `json:"fix_error,omitempty"`

	Fix func() error `json:"-"`
}

type Report struct {
	Findings []Finding `json:"findings"`
}

func (r *Report) MaxSeverity() Severity {
	m := OK
	for _, f := range r.Findings {
		if f.Severity > m {
			m = f.Severity
		}
	}
	return m
}

func (r *Report) AutoFixable() []*Finding {
	var out []*Finding
	for i := range r.Findings {
		if r.Findings[i].Fix != nil {
			out = append(out, &r.Findings[i])
		}
	}
	return out
}

type Runner struct {
	WsRoot string

	WS *config.Workspace

	Only string

	SkipRemote bool

	OnScope func(scope string, findings []Finding)
}

func (r *Runner) Run() *Report {
	rep := &Report{}
	emit := func(scope string, findings []Finding) {
		rep.Findings = append(rep.Findings, findings...)
		if r.OnScope != nil {
			r.OnScope(scope, findings)
		}
	}

	emit("system", r.systemChecks())
	for _, name := range r.projectNames() {
		proj := r.WS.Projects[name]
		emit(name, r.projectChecks(name, proj))
	}
	return rep
}

func (r *Runner) projectNames() []string {
	var names []string
	for name, p := range r.WS.Projects {
		if p.Status != config.StatusActive {
			continue
		}
		if r.Only != "" && name != r.Only {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ApplyFixes(rep *Report) int {
	fixed := 0
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if f.Fix == nil {
			continue
		}
		if err := f.Fix(); err != nil {
			f.FixError = err.Error()
			continue
		}
		f.Fixed = true
		fixed++
	}
	return fixed
}
