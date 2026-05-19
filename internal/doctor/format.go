package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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
