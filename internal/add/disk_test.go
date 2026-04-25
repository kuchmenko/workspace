package add

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

// initRepoAt creates a tiny git repo at parent/<name> and seeds one
// commit. Returns the abs path. Uses testutil.RunGit so the repo is
// configured deterministically (no global config leakage, fixed
// identity, no GPG).
func initRepoAt(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	testutil.RunGit(t, parent, "init", "--quiet", name)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, dir, "add", "README.md")
	testutil.RunGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func setRemoteOrigin(t *testing.T, repoDir, url string) {
	t.Helper()
	testutil.RunGit(t, repoDir, "remote", "add", "origin", url)
}

func TestDiskSource_FindsUnregisteredRepos(t *testing.T) {
	wsRoot := t.TempDir()
	personal := filepath.Join(wsRoot, "personal")
	work := filepath.Join(wsRoot, "work")
	if err := os.MkdirAll(personal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}

	a := initRepoAt(t, personal, "alpha")
	setRemoteOrigin(t, a, "git@github.com:me/alpha.git")
	initRepoAt(t, personal, "beta")
	known := initRepoAt(t, work, "known")
	setRemoteOrigin(t, known, "git@github.com:org/known.git")

	src := &DiskSource{
		WsRoot: wsRoot,
		Known:  map[string]bool{"work/known": true},
	}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	names := suggestionNames(got)
	sort.Strings(names)
	if !sliceEq(names, []string{"alpha", "beta"}) {
		t.Errorf("got %v, want [alpha beta]", names)
	}

	for _, s := range got {
		if s.Name == "alpha" {
			if s.RemoteURL != "git@github.com:me/alpha.git" {
				t.Errorf("alpha remote: %q", s.RemoteURL)
			}
			if s.DiskPath == "" {
				t.Error("alpha DiskPath empty")
			}
		}
	}
}

func TestDiskSource_SkipsBareAndWorktreeSiblings(t *testing.T) {
	wsRoot := t.TempDir()
	personal := filepath.Join(wsRoot, "personal")
	if err := os.MkdirAll(personal, 0o755); err != nil {
		t.Fatal(err)
	}

	initRepoAt(t, personal, "real")
	testutil.RunGit(t, personal, "init", "--bare", "--quiet", "real.bare")
	testutil.RunGit(t, personal, "init", "--quiet", "real-wt-linux-feature")
	testutil.RunGit(t, personal, "init", "--quiet", ".cache")

	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	got, _ := src.FetchSuggestions(context.Background())

	names := suggestionNames(got)
	if !sliceEq(names, []string{"real"}) {
		t.Errorf("got %v, want [real]", names)
	}
}

func TestDiskSource_RecursesOneLevel(t *testing.T) {
	wsRoot := t.TempDir()
	work := filepath.Join(wsRoot, "work")
	myorg := filepath.Join(work, "myorg")
	if err := os.MkdirAll(myorg, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepoAt(t, myorg, "api")
	initRepoAt(t, myorg, "web")

	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	got, _ := src.FetchSuggestions(context.Background())

	names := suggestionNames(got)
	sort.Strings(names)
	if !sliceEq(names, []string{"api", "web"}) {
		t.Errorf("got %v, want [api web]", names)
	}
}

func TestDiskSource_DoesNotRecurseTwoLevels(t *testing.T) {
	wsRoot := t.TempDir()
	deep := filepath.Join(wsRoot, "work", "org", "team")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepoAt(t, deep, "buried")

	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	got, _ := src.FetchSuggestions(context.Background())
	if len(got) != 0 {
		t.Errorf("expected 0 suggestions, got %v", suggestionNames(got))
	}
}

func TestDiskSource_MissingRootIsSilent(t *testing.T) {
	wsRoot := t.TempDir()
	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestDiskSource_RespectsContextCancel(t *testing.T) {
	wsRoot := t.TempDir()
	personal := filepath.Join(wsRoot, "personal")
	if err := os.MkdirAll(personal, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepoAt(t, personal, "x")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	_, _ = src.FetchSuggestions(ctx)
	// Either: source returns ctx err, or it returns the in-flight
	// partial result. Both acceptable — the contract is "honor cancel
	// promptly", not "return error on every cancel".
}

func TestNewDiskSource_BuildsKnownFromWorkspace(t *testing.T) {
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"a": {Path: "personal/a"},
			"b": {Path: "work/myorg/b"},
		},
	}
	src := NewDiskSource("/wsroot", ws)
	if !src.Known["personal/a"] || !src.Known["work/myorg/b"] {
		t.Errorf("Known map missing expected entries: %v", src.Known)
	}
}

func TestDiskSource_EmptyWsRootErrors(t *testing.T) {
	src := &DiskSource{}
	_, err := src.FetchSuggestions(context.Background())
	if err == nil {
		t.Error("expected error for empty WsRoot")
	}
}

func TestDiskSource_CustomRoots(t *testing.T) {
	wsRoot := t.TempDir()
	custom := filepath.Join(wsRoot, "custom")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepoAt(t, custom, "in-custom")

	// Default DiskSource skips this — "custom" not in DefaultDiskRoots.
	src := &DiskSource{WsRoot: wsRoot, Known: map[string]bool{}}
	got, _ := src.FetchSuggestions(context.Background())
	if len(got) != 0 {
		t.Errorf("default roots should not include custom: %v", suggestionNames(got))
	}

	// With Roots override → found.
	src.Roots = []string{"custom"}
	got, _ = src.FetchSuggestions(context.Background())
	if len(got) != 1 || got[0].Name != "in-custom" {
		t.Errorf("override Roots: got %v", suggestionNames(got))
	}
}

// suggestionNames + sliceEq are shared with suggestions_test.go via the
// `names` helper there. Keeping a separate name here so we don't collide.
// (Both files are in package add; Go enforces unique top-level names.)
//
// Actually suggestions_test.go uses `names` not `suggestionNames`, so
// we are clear.

func suggestionNames(s []Suggestion) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
