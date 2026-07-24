package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"github.com/spf13/cobra"
)

const (
	pathExitOK           = 0
	pathExitMissingDir   = 1
	pathExitUnknownProj  = 2
	pathExitUsage        = 64
	suggestionListCutoff = 5
)

func newPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path [project]",
		Short: "Print absolute path to the workspace root or a project",
		Long: `Resolve a project name to its absolute filesystem path.

With no argument, prints the workspace root. With one argument, prints
the joined absolute path of the named project.

Output is pure path on stdout (\n-terminated); errors go to stderr.

Designed for shell substitution, e.g.:

  cd "$(ws path workspace)"
  code "$(ws path myapp)"

Exit codes:
  0   success
  1   outside any workspace OR project registered but not cloned
  2   project name not present in workspace.toml
  64  usage error (more than one argument)`,
		Annotations: map[string]string{
			"capability": "observability",
			"agent:when": "Resolve a project name to its absolute filesystem path. With no argument, prints the workspace root. Designed for shell substitution: cd \"$(ws path foo)\".",
		},

		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintf(cmd.ErrOrStderr(), "ws path: too many arguments (got %d, want 0 or 1)\nusage: ws path [project]\n", len(args))
				osExit(pathExitUsage)
			}
			return nil
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 || ws == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names := make([]string, 0, len(ws.Projects))
			for name := range ws.Projects {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			code := runPath(cmd.OutOrStdout(), cmd.ErrOrStderr(), wsRoot, ws, args)
			if code != pathExitOK {
				osExit(code)
			}
			return nil
		},
	}
	return cmd
}

var osExit = os.Exit

func runPath(stdout, stderr io.Writer, wsRoot string, ws *config.Workspace, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wsRoot)
		return pathExitOK
	}
	name := args[0]
	proj, ok := ws.Projects[name]
	if !ok {
		writeUnknownProject(stderr, name, ws.Projects)
		return pathExitUnknownProj
	}
	abs := filepath.Join(wsRoot, proj.Path)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "ws path: not cloned: %q (path: %s)\nhint: ws bootstrap %s\n", name, proj.Path, name)
		return pathExitMissingDir
	}
	fmt.Fprintln(stdout, abs)
	return pathExitOK
}

func writeUnknownProject(w io.Writer, name string, projects map[string]config.Project) {
	fmt.Fprintf(w, "ws path: unknown project %q\n", name)
	if len(projects) == 0 || len(projects) >= suggestionListCutoff {
		return
	}
	names := make([]string, 0, len(projects))
	for n := range projects {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "\nregistered projects:")
	for _, n := range names {
		fmt.Fprintf(w, "  %s\n", n)
	}
}
