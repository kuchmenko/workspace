package doctor

import (
	"errors"
	"testing"
)

// Severity order is load-bearing — Report.MaxSeverity and the CLI exit
// code logic both depend on OK < Info < Warn < Error. Pin it explicitly
// so a future reorder is caught at test time, not at runtime.
func TestSeverityOrdering(t *testing.T) {
	got := []Severity{OK, Info, Warn, Error}
	for i := 0; i < len(got)-1; i++ {
		if got[i] >= got[i+1] {
			t.Fatalf("Severity %d (%s) >= %d (%s)", got[i], got[i], got[i+1], got[i+1])
		}
	}
}

func TestReportMaxSeverity(t *testing.T) {
	cases := []struct {
		name     string
		findings []Finding
		want     Severity
	}{
		{"empty", nil, OK},
		{"all ok", []Finding{{Severity: OK}, {Severity: OK}}, OK},
		{"ok and warn", []Finding{{Severity: OK}, {Severity: Warn}}, Warn},
		{"warn and error", []Finding{{Severity: Warn}, {Severity: Error}}, Error},
		{"only info", []Finding{{Severity: Info}}, Info},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := &Report{Findings: tc.findings}
			if got := rep.MaxSeverity(); got != tc.want {
				t.Fatalf("MaxSeverity=%s want %s", got, tc.want)
			}
		})
	}
}

// ApplyFixes runs every Fix and records the outcome in-place. Findings
// without a Fix must be left untouched; findings whose Fix returns an
// error must have FixError set and Fixed left false.
func TestApplyFixes(t *testing.T) {
	calls := map[string]int{}
	mkFix := func(name string, err error) func() error {
		return func() error {
			calls[name]++
			return err
		}
	}

	rep := &Report{Findings: []Finding{
		{Scope: "system", Check: "a", Severity: Warn, Fix: mkFix("a", nil)},
		{Scope: "system", Check: "b", Severity: Warn},
		{Scope: "x", Check: "c", Severity: Error, Fix: mkFix("c", errors.New("boom"))},
		{Scope: "x", Check: "d", Severity: Warn, Fix: mkFix("d", nil)},
	}}

	fixed := ApplyFixes(rep)
	if fixed != 2 {
		t.Fatalf("fixed=%d want 2", fixed)
	}
	if !rep.Findings[0].Fixed || rep.Findings[0].FixError != "" {
		t.Errorf("a: Fixed=%v FixError=%q", rep.Findings[0].Fixed, rep.Findings[0].FixError)
	}
	if rep.Findings[1].Fixed {
		t.Errorf("b: Fixed should be false when Fix is nil")
	}
	if rep.Findings[2].Fixed || rep.Findings[2].FixError == "" {
		t.Errorf("c: should have recorded FixError, got Fixed=%v FixError=%q",
			rep.Findings[2].Fixed, rep.Findings[2].FixError)
	}
	if !rep.Findings[3].Fixed {
		t.Errorf("d: Fixed=false")
	}
	if calls["a"] != 1 || calls["c"] != 1 || calls["d"] != 1 {
		t.Errorf("fix call counts: %v", calls)
	}
	if _, ok := calls["b"]; ok {
		t.Errorf("fix for b should not have run (no Fix attached)")
	}
}
