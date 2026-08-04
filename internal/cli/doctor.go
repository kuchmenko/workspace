package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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

	ConfigLoadErr error

	Only string

	SkipRemote bool

	OnScope func(scope string, findings []Finding)
}

func (r *Runner) Run(ctx context.Context) *Report {
	rep := &Report{}
	emit := func(scope string, findings []Finding) {
		rep.Findings = append(rep.Findings, findings...)
		if r.OnScope != nil {
			r.OnScope(scope, findings)
		}
	}

	emit("system", r.systemChecks())
	for _, name := range r.projectNames() {
		if ctx.Err() != nil {
			break
		}
		proj := r.WS.Projects[name]
		emit(name, r.projectChecks(ctx, name, proj))
	}
	return rep
}

func (r *Runner) projectNames() []string {
	if r.WS == nil {
		return nil
	}
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

var severitySymbol = map[Severity]string{
	OK:    "✓",
	Info:  "ℹ",
	Warn:  "⚠",
	Error: "✗",
}

func WriteText(w io.Writer, rep *Report) {
	groups := groupByScope(rep.Findings)
	fixable := fixableCount(rep.Findings)

	if findings, ok := groups["system"]; ok {
		writeBlock(w, "System", findings)
		delete(groups, "system")
	}

	for _, scope := range scopeOrder(rep.Findings) {
		if scope == "system" {
			continue
		}
		findings, ok := groups[scope]
		if !ok {
			continue
		}
		fmt.Fprintln(w)
		writeBlock(w, scope, findings)
	}

	WriteFooter(w, rep, fixable)
}

func WriteScope(w io.Writer, scope string, findings []Finding, leading bool) {
	if !leading {
		fmt.Fprintln(w)
	}
	writeBlock(w, scopeTitle(scope), findings)
}

func FixableCount(rep *Report) int {
	return fixableCount(rep.Findings)
}

func fixableCount(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if f.Fix != nil {
			n++
		}
	}
	return n
}

func scopeTitle(scope string) string {
	if scope == "system" {
		return "System"
	}
	return scope
}

func writeBlock(w io.Writer, title string, findings []Finding) {
	fmt.Fprintln(w, title)
	for _, f := range findings {
		sym := severitySymbol[f.Severity]
		fmt.Fprintf(w, "  %s %s: %s\n", sym, f.Check, f.Message)
		switch {
		case f.Fixed:
			fmt.Fprintf(w, "    → fix applied\n")
		case f.FixError != "":
			fmt.Fprintf(w, "    → fix failed: %s\n", f.FixError)
		case f.Fix != nil:
			fmt.Fprintf(w, "    → auto-fixable: %s\n", nonEmpty(f.FixHint, "run with --fix"))
		case f.FixHint != "":
			fmt.Fprintf(w, "    → %s\n", f.FixHint)
		}
	}
}

func WriteFooter(w io.Writer, rep *Report, fixable int) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("━", 21))
	stats := footerStats(rep)
	if stats.fixesApplied > 0 || stats.fixesFailed > 0 {
		writeFixSummary(w, stats)
		return
	}
	writeIssueSummary(w, stats.issues, fixable)
}

type footerCounts struct {
	issues       int
	fixesApplied int
	fixesFailed  int
}

func footerStats(rep *Report) footerCounts {
	var c footerCounts
	for _, f := range rep.Findings {
		if f.Severity >= Warn {
			c.issues++
		}
		if f.Fixed {
			c.fixesApplied++
		}
		if f.FixError != "" {
			c.fixesFailed++
		}
	}
	return c
}

func writeFixSummary(w io.Writer, stats footerCounts) {
	if stats.fixesApplied > 0 {
		fmt.Fprintf(w, "Applied %d fix(es).\n", stats.fixesApplied)
	}
	if stats.fixesFailed > 0 {
		fmt.Fprintf(w, "%d fix(es) failed — see messages above.\n", stats.fixesFailed)
	}
}

func writeIssueSummary(w io.Writer, issues, fixable int) {
	switch {
	case issues == 0:
		fmt.Fprintln(w, "All checks passed.")
	case fixable > 0:
		fmt.Fprintf(w, "%d issue(s) found (%d auto-fixable)\n", issues, fixable)
		fmt.Fprintln(w, "Run `ws doctor --fix` to apply safe fixes.")
	default:
		fmt.Fprintf(w, "%d issue(s) found.\n", issues)
	}
}

func WriteJSON(w io.Writer, rep *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(rep)
}

func groupByScope(findings []Finding) map[string][]Finding {
	out := map[string][]Finding{}
	for _, f := range findings {
		out[f.Scope] = append(out[f.Scope], f)
	}
	return out
}

func scopeOrder(findings []Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range findings {
		if seen[f.Scope] {
			continue
		}
		seen[f.Scope] = true
		out = append(out, f.Scope)
	}
	return out
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
