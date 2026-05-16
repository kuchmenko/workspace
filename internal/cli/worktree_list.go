package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [project]",
		Short: "List worktrees across projects",
		Annotations: map[string]string{
			"capability": "worktree",
			"agent:when": "List all worktrees across projects with branch, dirty/clean state, and ownership info",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			machine, _ := config.LoadMachineConfig()
			myMachine := ""
			if machine != nil {
				myMachine = machine.MachineName
			}

			var names []string
			if len(args) == 1 {
				names = []string{args[0]}
			} else {
				for n, p := range ws.Projects {
					if p.Status == config.StatusActive {
						names = append(names, n)
					}
				}
				sort.Strings(names)
			}

			fmt.Printf("%-20s %-50s %-30s %s\n", "PROJECT", "WORKTREE", "BRANCH", "STATE")
			for _, name := range names {
				proj, ok := ws.Projects[name]
				if !ok {
					continue
				}
				mainPath := filepath.Join(wsRoot, proj.Path)
				barePath := layout.BarePath(mainPath)
				if _, err := os.Stat(barePath); err != nil {
					fmt.Printf("%-20s %s\n", name, "(not migrated)")
					continue
				}
				wts, err := git.WorktreeList(barePath)
				if err != nil {
					fmt.Printf("%-20s ERROR %v\n", name, err)
					continue
				}
				for _, wt := range wts {
					if wt.Bare {
						continue
					}
					rel, _ := filepath.Rel(wsRoot, wt.Path)
					if rel == "" {
						rel = wt.Path
					}
					branchLabel := wt.Branch
					if wt.Detached {
						branchLabel = "(detached)"
					}
					state := worktreeStateString(&proj, wt, myMachine, proj.DefaultBranch)
					fmt.Printf("%-20s %-50s %-30s %s\n", name, rel, branchLabel, state)
				}
			}
			return nil
		},
	}
}

func worktreeStateString(proj *config.Project, wt git.Worktree, myMachine, defaultBranch string) string {
	parts := []string{}
	if git.IsDirty(wt.Path) {
		parts = append(parts, "DIRTY")
	} else {
		parts = append(parts, "clean")
	}
	if wt.Branch != "" {
		ahead, behind, has := git.AheadBehind(wt.Path, wt.Branch)
		if has {
			parts = append(parts, fmt.Sprintf("↑%d ↓%d", ahead, behind))
		} else {
			parts = append(parts, "no upstream")
		}
	}
	owner := "shared"
	switch {
	case wt.Branch == defaultBranch:
		owner = "main"
	case strings.HasPrefix(wt.Branch, "wt/"):
		owner = "legacy-wt"
	default:
		if meta := proj.LookupBranch(wt.Branch); meta != nil {
			myMine := false
			others := []string{}
			for _, m := range meta.Machines {
				if m == myMachine {
					myMine = true
					continue
				}
				others = append(others, m)
			}
			if myMine && len(others) == 0 {
				owner = "mine"
			} else if myMine {
				owner = "shared with " + strings.Join(others, ", ")
			} else if len(others) > 0 {
				owner = "remote (" + strings.Join(others, ", ") + ")"
			}
			if meta.LastActiveMachine != "" && meta.LastActiveAt != "" {
				if t, err := time.Parse(time.RFC3339, meta.LastActiveAt); err == nil {
					owner += fmt.Sprintf(" (last: %s %s)", meta.LastActiveMachine, t.Format("2006-01-02"))
				}
			}
		}
	}
	parts = append(parts, owner)
	return strings.Join(parts, ", ")
}
