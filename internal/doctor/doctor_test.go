package doctor

import (
	"errors"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
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

// Streaming contract: OnScope fires exactly once per scope, in the
// order system → project1 → project2, and each callback receives only
// that scope's findings (not cumulative). This is the guarantee the CLI
// relies on to render incremental output.
func TestRunner_OnScopeOrder(t *testing.T) {
	isolateState(t)
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"beta":  {Status: config.StatusActive, Remote: "x", Path: "beta"},
			"alpha": {Status: config.StatusActive, Remote: "x", Path: "alpha"},
			// Archived project must not produce a scope event.
			"gamma": {Status: config.StatusArchived, Remote: "x", Path: "gamma"},
		},
	}
	var gotScopes []string
	r := &Runner{
		WsRoot:     t.TempDir(),
		WS:         ws,
		SkipRemote: true,
		OnScope: func(scope string, findings []Finding) {
			gotScopes = append(gotScopes, scope)
			for _, f := range findings {
				if f.Scope != scope {
					t.Errorf("OnScope(%q) leaked finding for %q", scope, f.Scope)
				}
			}
		},
	}
	rep := r.Run()

	want := []string{"system", "alpha", "beta"}
	if len(gotScopes) != len(want) {
		t.Fatalf("got scopes=%v want %v", gotScopes, want)
	}
	for i := range want {
		if gotScopes[i] != want[i] {
			t.Fatalf("scope[%d]=%q want %q", i, gotScopes[i], want[i])
		}
	}

	// The streamed findings are the same slice the Report carries.
	// If Runner.Run ever diverges from its per-scope callback, tests
	// built on either one would start disagreeing.
	if len(rep.Findings) == 0 {
		t.Fatal("expected non-empty report")
	}
}
