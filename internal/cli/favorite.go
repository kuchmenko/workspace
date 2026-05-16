package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newFavoriteCmd builds the `ws favorite` command tree. Mirrors the
// `ws alias` shape (add/rm/list) so the two project-pinning surfaces
// — aliases for cd, favorites for `ws agent` — read consistently.
//
// Favorites are stored as `[projects.<name>].favorite = true` in
// workspace.toml, which means they sync across machines via the
// reconciler. The TUI hotkey `f` is the interactive equivalent.
func newFavoriteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "favorite",
		Short: "Pin projects to the Favorites section of `ws agent`",
		Long: `Manage the project favorites shown at the top of ` + "`" + `ws agent` + "`" + `.

Favorites are stored in workspace.toml and sync across machines via the
reconciler. The same toggle is available in the TUI as the f hotkey on
any project row.`,
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Pin / unpin projects shown in the Favorites section of `ws agent`",
		},
	}
	cmd.AddCommand(
		newFavoriteAddCmd(),
		newFavoriteRmCmd(),
		newFavoriteListCmd(),
	)
	return cmd
}

func newFavoriteAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <project>",
		Short: "Mark a project as favorite",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Pin a project to the Favorites section of `ws agent`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setProjectFavorite(args[0], true)
		},
	}
}

func newFavoriteRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <project>",
		Short: "Unmark a favorite project",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Unpin a project from the Favorites section of `ws agent`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setProjectFavorite(args[0], false)
		},
	}
}

func newFavoriteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List favorite projects",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Print favorited projects with their category and group",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			names := make([]string, 0)
			for n, p := range ws.Projects {
				if p.Favorite {
					names = append(names, n)
				}
			}
			if len(names) == 0 {
				fmt.Println("No favorites. Use `ws favorite add <project>` to pin one.")
				return nil
			}
			sort.Strings(names)
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCATEGORY\tGROUP")
			for _, n := range names {
				p := ws.Projects[n]
				group := p.Group
				if group == "" {
					group = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", n, p.Category, group)
			}
			return tw.Flush()
		},
	}
}

// setProjectFavorite updates the favorite flag on `name` and persists
// the workspace. Returns an error if the project is unknown or the
// save fails. No-op (with a printed notice) when the flag is already
// at the requested value — keeps the command idempotent for shell
// scripts that don't want to track current state.
func setProjectFavorite(name string, fav bool) error {
	p, ok := ws.Projects[name]
	if !ok {
		return fmt.Errorf("unknown project %q (see `ws status`)", name)
	}
	if !p.SetFavorite(fav) {
		if fav {
			fmt.Printf("%s is already a favorite.\n", name)
		} else {
			fmt.Printf("%s is not a favorite.\n", name)
		}
		return nil
	}
	ws.Projects[name] = p
	if err := saveWorkspace(); err != nil {
		return err
	}
	if fav {
		fmt.Printf("Added %s to favorites.\n", name)
	} else {
		fmt.Printf("Removed %s from favorites.\n", name)
	}
	return nil
}
