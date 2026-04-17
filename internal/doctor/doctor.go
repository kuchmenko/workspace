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

// Severity classifies how urgent a Finding is. The ordering matters: Report
// aggregates determine exit codes based on the highest severity observed.
type Severity int

const (
	// OK means the check passed. Still emitted so the user can see a full
	// picture of what was inspected.
	OK Severity = iota
	// Info is a neutral observation — e.g. a project classified as "self"
	// that doctor intentionally skips.
	Info
	// Warn is a non-blocking issue that the user should know about. The
	// daemon may continue to function but degraded.
	Warn
	// Error is a blocking problem. If left unfixed, core operations fail
	// (daemon stops syncing a project, push/pull cannot resolve upstream,
	// etc.).
	Error
)

// String returns the short symbolic form used by the text formatter.
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

// MarshalJSON emits the severity as its string form so JSON consumers
// (agents, scripts) see "ok"/"warn"/"error" rather than a brittle int
// that would shift if enum values are ever reordered.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON parses the string form back into the enum. Accepts the
// exact strings produced by MarshalJSON; anything else is rejected to
// make schema drift loud.
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

// Finding is one row in the doctor report. Fix is nil for findings that
// require the user to decide what to do (removing an index.lock, resolving
// a conflict, investigating a detached HEAD) — doctor never takes risky
// actions implicitly.
type Finding struct {
	// Scope is either "system" or a project name. Formatters group on it.
	Scope string `json:"scope"`
	// Check is the identifier from the catalog (e.g. "fetch-refspec").
	Check string `json:"check"`
	// Severity is OK / Info / Warn / Error.
	Severity Severity `json:"severity"`
	// Message is human-readable. Keep it to one line.
	Message string `json:"message"`
	// FixHint is a short suggestion for what to do — a command, or
	// "investigate X". Empty when Fix is non-nil and obvious from Message.
	FixHint string `json:"fix_hint,omitempty"`
	// Fixed is set by ApplyFixes when the attached Fix ran successfully.
	Fixed bool `json:"fixed,omitempty"`
	// FixError is set by ApplyFixes when the attached Fix returned an error.
	FixError string `json:"fix_error,omitempty"`
	// Fix is the auto-fix function. nil means "manual only". Not serialized.
	Fix func() error `json:"-"`
}

// Report is the collected output of a Runner pass.
type Report struct {
	Findings []Finding `json:"findings"`
}

// MaxSeverity returns the highest severity observed in the report. Used
// by the CLI to choose an exit code.
func (r *Report) MaxSeverity() Severity {
	m := OK
	for _, f := range r.Findings {
		if f.Severity > m {
			m = f.Severity
		}
	}
	return m
}

// AutoFixable returns every finding that has a non-nil Fix.
func (r *Report) AutoFixable() []*Finding {
	var out []*Finding
	for i := range r.Findings {
		if r.Findings[i].Fix != nil {
			out = append(out, &r.Findings[i])
		}
	}
	return out
}

// Runner drives one pass of system + project checks. Zero-value is not
// usable; callers must populate WsRoot and WS.
type Runner struct {
	// WsRoot is the absolute path of the workspace (same as config.FindRoot).
	WsRoot string
	// WS is the parsed workspace.toml.
	WS *config.Workspace
	// Only, when non-empty, restricts project checks to that single project.
	// System checks always run regardless.
	Only string
	// SkipRemote disables network-touching checks (remote-reach). Useful
	// for offline invocations and for tests.
	SkipRemote bool
	// OnScope, when non-nil, is invoked after each scope completes (first
	// with "system", then once per active project in sort order). The
	// findings slice passed in is the same one that lands in the returned
	// Report — callers can use it to stream progress to a terminal while
	// checks that touch the network (remote-reach) are still in flight
	// for later projects. Must not retain the slice past the call; the
	// Runner may append to it afterwards.
	OnScope func(scope string, findings []Finding)
}

// Run executes every check and returns the aggregated report. When
// OnScope is set it also streams findings per scope as they complete,
// so interactive callers can show progress without waiting for every
// project's network check to finish.
//
// The Runner does not mutate any state — callers are responsible for
// invoking ApplyFixes on the returned Report if --fix was requested.
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

// projectNames returns the names of projects the Runner should inspect,
// sorted for deterministic output. Only active projects are considered
// — archived and dormant projects are out of scope for doctor.
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

// ApplyFixes runs every Finding's Fix in report order and records the
// result in-place (Fixed / FixError). Returns the number of fixes that
// ran successfully. Findings without a Fix are skipped silently.
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
