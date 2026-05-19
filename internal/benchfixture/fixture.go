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

type Options struct {
	Projects int

	BranchesPerProject int

	Cloned bool

	AsGitRepo bool
}

type Workspace struct {
	Root        string
	ProjectList []Project
}

type Project struct {
	Name   string
	Path   string
	Remote string
}

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

func withDefaults(opts Options) Options {
	if opts.Projects <= 0 {
		opts.Projects = 10
	}
	if opts.BranchesPerProject <= 0 {
		opts.BranchesPerProject = 1
	}
	return opts
}

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

func writeWorkspaceTOML(tb testing.TB, root, body string) {
	tb.Helper()
	tomlPath := filepath.Join(root, "workspace.toml")
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		tb.Fatalf("write workspace.toml: %v", err)
	}
}

func initWorkspaceGit(tb testing.TB, root string) {
	tb.Helper()
	runGit(tb, root, "init", "-q", "-b", "main")
	runGit(tb, root, "add", "workspace.toml")
	runGit(tb, root, "commit", "-q", "-m", "init benchfixture workspace")
}

func buildFakeRemote(tb testing.TB, parent, name string) string {
	tb.Helper()
	bare := filepath.Join(parent, name+".git")
	runGit(tb, parent, "init", "-q", "--bare", "-b", "main", name+".git")

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

func cloneIntoLayout(tb testing.TB, root, projectPath, remoteBare string) {
	tb.Helper()
	mainPath := filepath.Join(root, projectPath)
	barePath := mainPath + ".bare"

	runGit(tb, root, "clone", "-q", "--bare", remoteBare, barePath)

	runGit(tb, barePath, "config", "remote.origin.fetch",
		"+refs/heads/*:refs/remotes/origin/*")
	runGit(tb, barePath, "worktree", "add", "-q", mainPath, "main")
}

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
