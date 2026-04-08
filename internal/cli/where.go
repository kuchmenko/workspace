package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/spf13/cobra"
)

// whereOutput is the stable JSON schema for `ws where --json`. Field names
// here are part of the public API; do not rename without bumping a version.
type whereOutput struct {
	InWorkspace   bool             `json:"in_workspace"`
	InProject     bool             `json:"in_project"`
	WorkspaceRoot string           `json:"workspace_root"`
	Cwd           string           `json:"cwd"`
	RelToRoot     string           `json:"rel_to_root,omitempty"`
	Category      string           `json:"category,omitempty"`
	Project       *whereProject    `json:"project,omitempty"`
	Worktree      *whereWorktree   `json:"worktree,omitempty"`
	Siblings      []whereSibling   `json:"siblings"`
	Neighbors     []whereNeighbor  `json:"neighbors"`
}

type whereProject struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	DefaultBranch string `json:"default_branch"`
	MainPath      string `json:"main_path"`
}

type whereWorktree struct {
	Path        string `json:"path"`
	IsMain      bool   `json:"is_main"`
	Branch      string `json:"branch"`
	Owner       string `json:"owner"`
	Dirty       bool   `json:"dirty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	HasUpstream bool   `json:"has_upstream"`
	Detached    bool   `json:"detached"`
}

type whereSibling struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Owner    string `json:"owner"`
	Detached bool   `json:"detached"`
}

type whereNeighbor struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Category string `json:"category"`
	Status   string `json:"status"`
}

func newWhereCmd() *cobra.Command {
	var (
		flagJSON      bool
		flagSiblings  bool
		flagNeighbors bool
		flagAll       bool
	)
	cmd := &cobra.Command{
		Use:   "where",
		Short: "Show where you are in the workspace tree (project, worktree, branch)",
		Long: `Show the current position inside the workspace: which project and
worktree you're in, branch state, and optionally sibling worktrees and
neighboring projects. Read-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagAll {
				flagSiblings = true
				flagNeighbors = true
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			cwd, _ = filepath.Abs(cwd)

			out := whereOutput{
				InWorkspace:   true,
				WorkspaceRoot: wsRoot,
				Cwd:           cwd,
			}
			if rel, err := filepath.Rel(wsRoot, cwd); err == nil && !strings.HasPrefix(rel, "..") {
				out.RelToRoot = rel
				if i := strings.IndexByte(rel, filepath.Separator); i > 0 {
					out.Category = rel[:i]
				} else if rel != "." {
					out.Category = rel
				}
			}

			// Prefix-match cwd against each project's main worktree and
			// extra-worktree directory prefix. No git calls in this loop.
			var (
				matchName string
				matchProj config.Project
				mainPath  string
				barePath  string
			)
			for name, p := range ws.Projects {
				if p.Status != config.StatusActive {
					continue
				}
				main := filepath.Join(wsRoot, p.Path)
				bare := layout.BarePath(main)
				if cwd == main || strings.HasPrefix(cwd, main+string(filepath.Separator)) {
					matchName, matchProj, mainPath, barePath = name, p, main, bare
					break
				}
				parent := filepath.Dir(main)
				base := filepath.Base(main)
				wtPrefix := filepath.Join(parent, base+"-wt-")
				if strings.HasPrefix(cwd, wtPrefix) {
					matchName, matchProj, mainPath, barePath = name, p, main, bare
					break
				}
			}

			machine, _ := config.LoadMachineConfig()
			myMachine := ""
			if machine != nil {
				myMachine = machine.MachineName
			}

			if matchName != "" {
				out.InProject = true
				out.Project = &whereProject{
					Name:          matchName,
					Category:      string(matchProj.Category),
					DefaultBranch: matchProj.DefaultBranch,
					MainPath:      matchProj.Path,
				}

				// Determine which exact worktree we're in via WorktreeList.
				// Falls back to a synthetic entry if the bare is missing
				// (project not migrated yet) — still useful info.
				var wts []git.Worktree
				if _, statErr := os.Stat(barePath); statErr == nil {
					wts, _ = git.WorktreeList(barePath)
				}

				var hit *git.Worktree
				for i := range wts {
					if wts[i].Bare {
						continue
					}
					wp := wts[i].Path
					if cwd == wp || strings.HasPrefix(cwd, wp+string(filepath.Separator)) {
						hit = &wts[i]
						break
					}
				}

				if hit != nil {
					ahead, behind, hasUp := git.AheadBehind(hit.Path, hit.Branch)
					ww := &whereWorktree{
						Path:        hit.Path,
						IsMain:      hit.Path == mainPath,
						Branch:      hit.Branch,
						Owner:       ownerOf(hit.Branch, matchProj.DefaultBranch, myMachine),
						Dirty:       git.IsDirty(hit.Path),
						Ahead:       ahead,
						Behind:      behind,
						HasUpstream: hasUp,
						Detached:    hit.Detached,
					}
					out.Worktree = ww
				} else {
					// Fallback: not migrated, or cwd inside a path not yet
					// registered as a worktree by git.
					out.Worktree = &whereWorktree{
						Path:   cwd,
						IsMain: cwd == mainPath,
					}
				}

				if flagSiblings {
					for _, w := range wts {
						if w.Bare || (out.Worktree != nil && w.Path == out.Worktree.Path) {
							continue
						}
						out.Siblings = append(out.Siblings, whereSibling{
							Path:     w.Path,
							Branch:   w.Branch,
							Owner:    ownerOf(w.Branch, matchProj.DefaultBranch, myMachine),
							Detached: w.Detached,
						})
					}
					if out.Siblings == nil {
						out.Siblings = []whereSibling{}
					}
				}
			}

			if flagNeighbors {
				cat := ""
				if out.Project != nil {
					cat = out.Project.Category
				} else {
					cat = out.Category
				}
				if cat != "" {
					var names []string
					for n, p := range ws.Projects {
						if string(p.Category) != cat {
							continue
						}
						if matchName != "" && n == matchName {
							continue
						}
						names = append(names, n)
					}
					sort.Strings(names)
					for _, n := range names {
						p := ws.Projects[n]
						out.Neighbors = append(out.Neighbors, whereNeighbor{
							Name:     n,
							Path:     p.Path,
							Category: string(p.Category),
							Status:   string(p.Status),
						})
					}
				}
				if out.Neighbors == nil {
					out.Neighbors = []whereNeighbor{}
				}
			}

			if flagJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			printWhereHuman(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagJSON, "json", false, "emit a stable JSON document for scripts/agents")
	cmd.Flags().BoolVarP(&flagSiblings, "siblings", "s", false, "include other worktrees of the current project")
	cmd.Flags().BoolVarP(&flagNeighbors, "neighbors", "n", false, "include neighboring projects in the same category")
	cmd.Flags().BoolVarP(&flagAll, "all", "a", false, "include siblings and neighbors")
	return cmd
}

func ownerOf(branch, defaultBranch, myMachine string) string {
	switch {
	case branch == "":
		return ""
	case branch == defaultBranch:
		return "main"
	case myMachine != "" && strings.HasPrefix(branch, layout.BranchPrefix(myMachine)):
		return "mine"
	case strings.HasPrefix(branch, "wt/"):
		return "remote"
	default:
		return "shared"
	}
}

func printWhereHuman(w interface{ Write([]byte) (int, error) }, o whereOutput) {
	pf := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	if !o.InProject {
		pf("workspace: %s\n", o.WorkspaceRoot)
		if o.Category != "" {
			pf("category:  %s\n", o.Category)
		}
		if o.RelToRoot != "" && o.RelToRoot != "." {
			pf("cwd:       %s   (no project here)\n", o.RelToRoot)
		} else {
			pf("cwd:       %s\n", o.Cwd)
		}
		pf("hint:      run `ws list` to see registered projects\n")
		printNeighbors(w, o)
		return
	}

	pf("project:  %s (%s)\n", o.Project.Name, o.Project.Category)
	pf("root:     %s\n", o.WorkspaceRoot)
	if o.Worktree != nil {
		kind := "extra worktree"
		if o.Worktree.IsMain {
			kind = "main worktree"
		}
		rel, _ := filepath.Rel(o.WorkspaceRoot, o.Worktree.Path)
		if rel == "" {
			rel = o.Worktree.Path
		}
		pf("worktree: %s (%s)\n", rel, kind)
		branchLabel := o.Worktree.Branch
		if o.Worktree.Detached {
			branchLabel = "(detached)"
		}
		if branchLabel == "" {
			branchLabel = "(unknown — not migrated?)"
		}
		pf("branch:   %s\n", branchLabel)
		state := []string{}
		if o.Worktree.Dirty {
			state = append(state, "DIRTY")
		} else if o.Worktree.Branch != "" {
			state = append(state, "clean")
		}
		if o.Worktree.HasUpstream {
			state = append(state, fmt.Sprintf("↑%d ↓%d", o.Worktree.Ahead, o.Worktree.Behind))
		} else if o.Worktree.Branch != "" {
			state = append(state, "no upstream")
		}
		if o.Worktree.Owner != "" {
			state = append(state, "owner="+o.Worktree.Owner)
		}
		if len(state) > 0 {
			pf("state:    %s\n", strings.Join(state, ", "))
		}
	}

	if len(o.Siblings) > 0 {
		pf("\nsiblings:\n")
		for _, s := range o.Siblings {
			rel, _ := filepath.Rel(o.WorkspaceRoot, s.Path)
			if rel == "" {
				rel = s.Path
			}
			b := s.Branch
			if s.Detached {
				b = "(detached)"
			}
			pf("  %-50s %-30s %s\n", rel, b, s.Owner)
		}
	} else if o.Siblings != nil {
		pf("\nsiblings: (none)\n")
	}

	printNeighbors(w, o)
}

func printNeighbors(w interface{ Write([]byte) (int, error) }, o whereOutput) {
	if o.Neighbors == nil {
		return
	}
	pf := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	if len(o.Neighbors) == 0 {
		pf("\nneighbors: (none)\n")
		return
	}
	pf("\nneighbors:\n")
	for _, n := range o.Neighbors {
		pf("  %-25s %s\n", n.Name, n.Path)
	}
}
