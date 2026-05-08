package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Symbols used in the text formatter. Kept ASCII so terminals without
// unicode support stay readable; the tick/cross are in the BMP and
// render fine on any modern terminal.
var severitySymbol = map[Severity]string{
	OK:    "✓",
	Info:  "ℹ",
	Warn:  "⚠",
	Error: "✗",
}

// WriteText renders the report in the human-readable format shown in the
// issue. Grouping is: "System" block first, then one block per project
// in insertion order. Findings inside a block are printed in the order
// the Runner produced them, which is the catalog order — stable across
// runs.
//
// When a fix was attempted (either applied or failed) the line is
// annotated so the user can see the outcome at a glance without rerunning.
//
// For interactive runs prefer WriteScope + WriteFooter so blocks
// stream as each scope completes.
func WriteText(w io.Writer, rep *Report) {
	groups := groupByScope(rep.Findings)
	fixable := fixableCount(rep.Findings)

	// Print the system block first if it exists.
	if findings, ok := groups["system"]; ok {
		writeBlock(w, "System", findings)
		delete(groups, "system")
	}

	// Remaining blocks in scope-order (matches Runner.projectNames sort).
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

// WriteScope renders a single scope block exactly as WriteText would.
// Used by the CLI command to stream per-scope results as the Runner
// completes each check batch. Pass leading=true for the first scope of
// a run so no leading blank line is emitted (the "System" header
// should sit flush against the top of the output).
func WriteScope(w io.Writer, scope string, findings []Finding, leading bool) {
	if !leading {
		fmt.Fprintln(w)
	}
	writeBlock(w, scopeTitle(scope), findings)
}

// FixableCount returns the number of findings in rep that advertise an
// auto-fix. Exposed so the CLI can compute the footer-line suffix
// ("N auto-fixable") after streaming has already flushed each block.
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

// scopeTitle maps the Runner's scope identifier to the user-facing
// heading. "system" becomes "System"; project names are displayed as-is.
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

// WriteFooter is the summary line at the bottom of the report. Its shape
// changes depending on whether any fixes were applied — the acceptance
// criteria distinguishes "before --fix" ("N issues found, K auto-fixable")
// from "after --fix" ("Applied K fixes"). Exposed so the streaming
// caller can flush its final line after every scope has already been
// written.
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

// footerCounts is the aggregate the footer renderer needs from a
// Report: count of issues (severity ≥ Warn), count of successfully-
// applied fixes, count of attempted-but-failed fixes.
type footerCounts struct {
	issues       int
	fixesApplied int
	fixesFailed  int
}

// footerStats walks the findings once and tallies the three counters
// the footer cares about. Single pass, no second sort.
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

// writeFixSummary handles the post-`--fix` footer: "Applied N fixes"
// and / or "M fixes failed" depending on which counters are non-zero.
// Used only when at least one fix was attempted.
func writeFixSummary(w io.Writer, stats footerCounts) {
	if stats.fixesApplied > 0 {
		fmt.Fprintf(w, "Applied %d fix(es).\n", stats.fixesApplied)
	}
	if stats.fixesFailed > 0 {
		fmt.Fprintf(w, "%d fix(es) failed — see messages above.\n", stats.fixesFailed)
	}
}

// writeIssueSummary is the no-fix-attempted footer. Three branches:
// clean ("All checks passed"), unfixable issues, or some-auto-fixable
// (with a hint at `ws doctor --fix`).
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

// WriteJSON serializes the report. Fix functions are not part of the
// output (Finding.Fix has a `json:"-"` tag); the presence of an
// auto-fix is conveyed by the "fix_hint" field plus the top-level
// meta summary.
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

// scopeOrder returns scopes in the order they first appear in findings.
// This preserves the Runner's project-name sort for the report output
// without needing a second sort pass.
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
