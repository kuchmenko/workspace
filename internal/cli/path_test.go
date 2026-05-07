package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

// fixtureWorkspace builds a Workspace registry with two projects whose
// paths are anchored under root. cloned=true creates the directory; the
// other project is registered-but-missing.
func fixtureWorkspace(t *testing.T, root string) *config.Workspace {
	t.Helper()
	clonedRel := "personal/cloned"
	if err := os.MkdirAll(filepath.Join(root, clonedRel), 0o755); err != nil {
		t.Fatalf("mkdir cloned: %v", err)
	}
	return &config.Workspace{
		Projects: map[string]config.Project{
			"cloned": {
				Path:     clonedRel,
				Status:   config.StatusActive,
				Category: config.CategoryPersonal,
			},
			"missing": {
				Path:     "personal/missing",
				Status:   config.StatusActive,
				Category: config.CategoryPersonal,
			},
		},
	}
}

func TestRunPath_NoArgPrintsRoot(t *testing.T) {
	root := t.TempDir()
	ws := fixtureWorkspace(t, root)
	var stdout, stderr bytes.Buffer

	code := runPath(&stdout, &stderr, root, ws, nil)

	if code != pathExitOK {
		t.Fatalf("exit code = %d, want %d", code, pathExitOK)
	}
	if got := strings.TrimRight(stdout.String(), "\n"); got != root {
		t.Fatalf("stdout = %q, want %q", got, root)
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("stdout missing trailing newline: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr should be empty, got %q", stderr.String())
	}
}

func TestRunPath_RegisteredAndCloned(t *testing.T) {
	root := t.TempDir()
	ws := fixtureWorkspace(t, root)
	var stdout, stderr bytes.Buffer

	code := runPath(&stdout, &stderr, root, ws, []string{"cloned"})

	want := filepath.Join(root, "personal/cloned")
	if code != pathExitOK {
		t.Fatalf("exit code = %d, want %d (stderr=%q)", code, pathExitOK, stderr.String())
	}
	if got := strings.TrimRight(stdout.String(), "\n"); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr should be empty, got %q", stderr.String())
	}
}

func TestRunPath_RegisteredButMissingDir(t *testing.T) {
	root := t.TempDir()
	ws := fixtureWorkspace(t, root)
	var stdout, stderr bytes.Buffer

	code := runPath(&stdout, &stderr, root, ws, []string{"missing"})

	if code != pathExitMissingDir {
		t.Fatalf("exit code = %d, want %d", code, pathExitMissingDir)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be silent, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `not cloned: "missing"`) {
		t.Fatalf("stderr missing 'not cloned' line: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ws bootstrap missing") {
		t.Fatalf("stderr missing bootstrap hint: %q", stderr.String())
	}
}

func TestRunPath_UnknownProject(t *testing.T) {
	root := t.TempDir()
	ws := fixtureWorkspace(t, root)
	var stdout, stderr bytes.Buffer

	code := runPath(&stdout, &stderr, root, ws, []string{"no-such-thing"})

	if code != pathExitUnknownProj {
		t.Fatalf("exit code = %d, want %d", code, pathExitUnknownProj)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be silent, got %q", stdout.String())
	}
	s := stderr.String()
	if !strings.Contains(s, `unknown project "no-such-thing"`) {
		t.Fatalf("stderr missing 'unknown project' line: %q", s)
	}
	// Two registered projects → under cutoff, both should be listed.
	if !strings.Contains(s, "  cloned\n") || !strings.Contains(s, "  missing\n") {
		t.Fatalf("stderr should list registered projects, got %q", s)
	}
}

// When the registry is at/above the suggestion cutoff, the error message
// stays terse — listing 5+ names would be noise.
func TestRunPath_UnknownProject_LargeRegistryNoSuggestions(t *testing.T) {
	root := t.TempDir()
	ws := &config.Workspace{Projects: map[string]config.Project{}}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		ws.Projects[n] = config.Project{Path: filepath.Join("personal", n)}
	}
	var stdout, stderr bytes.Buffer

	code := runPath(&stdout, &stderr, root, ws, []string{"zzz"})

	if code != pathExitUnknownProj {
		t.Fatalf("exit code = %d, want %d", code, pathExitUnknownProj)
	}
	s := stderr.String()
	if !strings.Contains(s, `unknown project "zzz"`) {
		t.Fatalf("stderr missing error line: %q", s)
	}
	if strings.Contains(s, "registered projects:") {
		t.Fatalf("stderr should not list projects when registry >= cutoff, got %q", s)
	}
}

// TestPathCmd_TooManyArgs covers the cobra Args validator path. It runs
// the assembled command and traps osExit via a panic-based stub.
func TestPathCmd_TooManyArgs(t *testing.T) {
	root := t.TempDir()
	ws = fixtureWorkspace(t, root)
	wsRoot = root
	t.Cleanup(func() { ws = nil; wsRoot = "" })

	type exitPanic struct{ code int }
	prev := osExit
	osExit = func(code int) { panic(exitPanic{code}) }
	t.Cleanup(func() { osExit = prev })

	cmd := newPathCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"foo", "bar"})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected osExit to fire; stderr=%q", stderr.String())
		}
		ep, ok := r.(exitPanic)
		if !ok {
			t.Fatalf("unexpected panic: %v", r)
		}
		if ep.code != pathExitUsage {
			t.Fatalf("exit code = %d, want %d", ep.code, pathExitUsage)
		}
		if !strings.Contains(stderr.String(), "too many arguments") {
			t.Fatalf("stderr missing usage error: %q", stderr.String())
		}
	}()
	_ = cmd.Execute()
}
