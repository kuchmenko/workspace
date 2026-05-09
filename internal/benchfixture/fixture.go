// Package benchfixture provides synthetic workspace builders for L2
// macrobenchmarks. The harness constructs a hermetic workspace under
// tb.TempDir() with the bare+worktree layout that the reconciler expects,
// using real git for verisimilitude (mocks would mask the bugs we're
// trying to catch).
//
// Files in this package are tagged `//go:build bench_l2` so they don't
// run on `go test ./...`. They're picked up only by:
//
//	go test -tags=bench_l2 -bench=. ./internal/benchfixture/...
//
// which is what `bench/scripts/run-l2.sh` does.
package benchfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Options configures a synthetic workspace.
type Options struct {
	// Projects is the number of [projects.N] entries to register.
	Projects int

	// BranchesPerProject is how many [[projects.N.branches]] entries
	// each project gets. Default 1.
	BranchesPerProject int

	// Cloned, when true, runs a real `git clone --bare` per project so
	// the bare+worktree layout is present on disk. Set true for benches
	// that exercise reconciler.reconcileProjects; false for scan-only
	// benches that just need workspace.toml + flat directories.
	Cloned bool

	// AsGitRepo, when true, runs `git init` in the workspace root and
	// does an initial commit of workspace.toml. Required for benches
	// that exercise reconciler.syncTOML; otherwise Phase 1 no-ops.
	AsGitRepo bool
}

// Workspace is the result of Build.
type Workspace struct {
	Root        string
	ProjectList []Project
}

// Project describes one synthetic project for the bench.
type Project struct {
	Name   string
	Path   string // relative to Workspace.Root, e.g. "personal/proj-0"
	Remote string // file:// URL of fake remote
}

// Build constructs a synthetic workspace with the given options. It uses
// `tb.TempDir()` so cleanup is automatic. All git invocations use a
// hermetic env (no global config, no GPG, fixed identity) inherited from
// the test fixture pattern in internal/testutil.
func Build(tb testing.TB, opts Options) *Workspace {
	tb.Helper()
	opts = withDefaults(opts)
	root, remotes := prepareRoots(tb)
	ws := &Workspace{Root: root}

	tomlBody := composeProjects(tb, ws, opts, root, remotes)
	writeWorkspaceTOML(tb, root, tomlBody)

	if opts.AsGitRepo {
		initWorkspaceGit(tb, root)
	}
	return ws
}

// withDefaults applies non-destructive defaults to a zero-valued
// Options. Centralized so callers can pass `Options{}` and get a
// sensible 10-project fixture.
func withDefaults(opts Options) Options {
	if opts.Projects <= 0 {
		opts.Projects = 10
	}
	if opts.BranchesPerProject <= 0 {
		opts.BranchesPerProject = 1
	}
	return opts
}

// prepareRoots creates the workspace tempdir and the `.remotes/` cache
// dir for fake remotes. Returns (root, remotesDir).
func prepareRoots(tb testing.TB) (string, string) {
	tb.Helper()
	root := tb.TempDir()
	remotes := filepath.Join(root, ".remotes")
	if err := os.MkdirAll(filepath.Join(root, "personal"), 0o755); err != nil {
		tb.Fatalf("mkdir personal: %v", err)
	}
	if err := os.MkdirAll(remotes, 0o755); err != nil {
		tb.Fatalf("mkdir remotes: %v", err)
	}
	return root, remotes
}

// composeProjects creates fake remotes, optionally clones each into the
// bare+worktree layout, appends to `ws.ProjectList`, and returns the
// rendered workspace.toml body. Single-pass so caller does only one
// write.
func composeProjects(tb testing.TB, ws *Workspace, opts Options, root, remotes string) string {
	tb.Helper()
	var sb strings.Builder
	sb.WriteString("[meta]\nversion = 1\nroot = \".\"\n\n")
	sb.WriteString("[daemon]\npoll_interval = \"5m\"\nstale_threshold = \"30d\"\nauto_sync = true\nwatch_dirs = false\n\n")

	for i := 0; i < opts.Projects; i++ {
		name := fmt.Sprintf("proj-%03d", i)
		path := filepath.Join("personal", name)
		remote := buildFakeRemote(tb, remotes, name)

		ws.ProjectList = append(ws.ProjectList, Project{
			Name:   name,
			Path:   path,
			Remote: "file://" + remote,
		})
		writeProjectTOMLEntry(&sb, name, path, remote, opts.BranchesPerProject)

		if opts.Cloned {
			cloneIntoLayout(tb, root, path, remote)
		}
	}
	return sb.String()
}

// writeProjectTOMLEntry appends one [projects.<name>] block plus its
// [[projects.<name>.branches]] list to sb. Pure formatting — no IO.
func writeProjectTOMLEntry(sb *strings.Builder, name, path, remote string, branches int) {
	fmt.Fprintf(sb, "[projects.%s]\n", name)
	fmt.Fprintf(sb, "remote = %q\n", "file://"+remote)
	fmt.Fprintf(sb, "path = %q\n", path)
	fmt.Fprintf(sb, "status = \"active\"\n")
	fmt.Fprintf(sb, "category = \"personal\"\n")
	fmt.Fprintf(sb, "default_branch = \"main\"\n\n")
	for j := 0; j < branches; j++ {
		fmt.Fprintf(sb, "[[projects.%s.branches]]\n", name)
		fmt.Fprintf(sb, "  name = \"feat/branch-%d\"\n", j)
		fmt.Fprintf(sb, "  machines = [\"bench-machine\"]\n")
		fmt.Fprintf(sb, "  created_by = \"bench-machine\"\n\n")
	}
}

// writeWorkspaceTOML drops the rendered TOML body at the canonical
// location. Failure is fatal — fixture builds are deterministic.
func writeWorkspaceTOML(tb testing.TB, root, body string) {
	tb.Helper()
	tomlPath := filepath.Join(root, "workspace.toml")
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		tb.Fatalf("write workspace.toml: %v", err)
	}
}

// initWorkspaceGit turns the workspace root into a git repo with
// workspace.toml committed. Required only by benches that exercise
// reconciler.syncTOML (Phase 1).
func initWorkspaceGit(tb testing.TB, root string) {
	tb.Helper()
	runGit(tb, root, "init", "-q", "-b", "main")
	runGit(tb, root, "add", "workspace.toml")
	runGit(tb, root, "commit", "-q", "-m", "init benchfixture workspace")
}

// buildFakeRemote creates a bare git repo with a single commit on `main`.
// Returns the absolute path; caller wraps with file:// for the URL form.
func buildFakeRemote(tb testing.TB, parent, name string) string {
	tb.Helper()
	bare := filepath.Join(parent, name+".git")
	runGit(tb, parent, "init", "-q", "--bare", "-b", "main", name+".git")

	// Seed via a working clone — bare repos can't accept commits directly.
	work := filepath.Join(parent, name+".work")
	runGit(tb, parent, "clone", "-q", bare, name+".work")
	if err := os.WriteFile(filepath.Join(work, "README.md"),
		[]byte("# "+name+"\n"), 0o644); err != nil {
		tb.Fatalf("write README in seed: %v", err)
	}
	runGit(tb, work, "add", "README.md")
	runGit(tb, work, "commit", "-q", "-m", "seed")
	runGit(tb, work, "push", "-q", "origin", "main")
	_ = os.RemoveAll(work)

	return bare
}

// cloneIntoLayout reproduces the bare+worktree layout inline (without
// pulling in internal/clone, which would create a circular dependency
// when reconciler benches eventually live here too). Layout matches what
// `ws migrate` produces post-conversion.
func cloneIntoLayout(tb testing.TB, root, projectPath, remoteBare string) {
	tb.Helper()
	mainPath := filepath.Join(root, projectPath)
	barePath := mainPath + ".bare"

	runGit(tb, root, "clone", "-q", "--bare", remoteBare, barePath)
	// fetch refspec is missing on `clone --bare` (matches CloneIntoLayout).
	runGit(tb, barePath, "config", "remote.origin.fetch",
		"+refs/heads/*:refs/remotes/origin/*")
	runGit(tb, barePath, "worktree", "add", "-q", mainPath, "main")
}

// runGit executes a git command with hermetic env. Failures are tb.Fatal.
func runGit(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = []string{
		"GIT_AUTHOR_NAME=ws-bench",
		"GIT_AUTHOR_EMAIL=bench@example.invalid",
		"GIT_COMMITTER_NAME=ws-bench",
		"GIT_COMMITTER_EMAIL=bench@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + os.TempDir(),
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %s in %s: %v\n%s",
			strings.Join(args, " "), dir, err, string(out))
	}
}
