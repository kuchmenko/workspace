package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show all projects with their current state",
		Annotations: map[string]string{
			"capability": "observability",
			"agent:when": "Get an overview of all projects: branch, last commit, layout (plain/worktree/missing)",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Projects) == 0 {
				fmt.Println("No projects registered. Use 'ws add <url>' to add one.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tGROUP\tSTATUS\tBRANCH\tLAST COMMIT\tLAYOUT")

			// Sort projects by name
			names := make([]string, 0, len(ws.Projects))
			for name := range ws.Projects {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				proj := ws.Projects[name]
				absPath := filepath.Join(wsRoot, proj.Path)

				branch := "-"
				lastCommit := "-"
				layoutInfo := "missing"

				if info, err := os.Stat(absPath); err == nil && info.IsDir() {
					layoutInfo = "plain"
					if git.IsRepo(absPath) {
						if b, err := git.CurrentBranch(absPath); err == nil {
							branch = b
						}
						if t, err := git.LastCommitTime(absPath); err == nil {
							lastCommit = humanizeTime(t)
						}
					}
				}
				if _, err := os.Stat(layout.BarePath(absPath)); err == nil {
					// Migrated. Count extra worktrees by enumerating siblings.
					n := countExtraWorktrees(absPath)
					if n > 0 {
						layoutInfo = fmt.Sprintf("worktree+%d", n)
					} else {
						layoutInfo = "worktree"
					}
				}

				groupDisplay := proj.Group
				if groupDisplay == "" {
					groupDisplay = string(proj.Category)
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					name, groupDisplay, proj.Status, branch, lastCommit, layoutInfo)
			}

			return w.Flush()
		},
	}
}

// countExtraWorktrees counts sibling directories of mainPath that match the
// "<base>-wt-*" naming convention. Cheap O(N) scan of the parent directory.
func countExtraWorktrees(mainPath string) int {
	parent := filepath.Dir(mainPath)
	base := filepath.Base(mainPath) + "-wt-"
	entries, err := os.ReadDir(parent)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), base) {
			n++
		}
	}
	return n
}

func humanizeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	default:
		return t.Format("2006-01-02")
	}
}

// staleness returns a human-readable staleness indicator.
func staleness(t time.Time, threshold string) string {
	threshDur := parseDuration(threshold)
	if threshDur == 0 {
		threshDur = 30 * 24 * time.Hour
	}
	d := time.Since(t)
	if d > threshDur {
		return fmt.Sprintf("stale (%s)", humanizeTime(t))
	}
	return ""
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
		var days int
		fmt.Sscanf(s, "%d", &days)
		return time.Duration(days) * 24 * time.Hour
	}
	d, _ := time.ParseDuration(s)
	return d
}
