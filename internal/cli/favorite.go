package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/spf13/cobra"
)

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
		Use:   "add <project | @group>",
		Short: "Mark a project or group as favorite",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Pin a project or group to the quick-nav chips of `ws explorer`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setFavorite(args[0], true)
		},
	}
}

func newFavoriteRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <project | @group>",
		Short: "Unmark a favorite project or group",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Unpin a project or group from the quick-nav chips of `ws explorer`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setFavorite(args[0], false)
		},
	}
}

func setFavorite(arg string, fav bool) error {
	if len(arg) > 1 && arg[0] == '@' {
		return setGroupFavoriteCLI(arg[1:], fav)
	}
	return setProjectFavorite(arg, fav)
}

func setGroupFavoriteCLI(name string, fav bool) error {
	if _, ok := ws.Groups[name]; !ok {
		if ws.Groups == nil {
			ws.Groups = map[string]config.Group{}
		}
		ws.Groups[name] = config.Group{}
	}
	if !ws.SetGroupFavorite(name, fav) {
		if fav {
			fmt.Printf("@%s is already a favorite.\n", name)
		} else {
			fmt.Printf("@%s is not a favorite.\n", name)
		}
		return nil
	}
	if err := saveWorkspace(); err != nil {
		return err
	}
	if fav {
		fmt.Printf("Added @%s to favorites.\n", name)
	} else {
		fmt.Printf("Removed @%s from favorites.\n", name)
	}
	return nil
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
			var projNames, groupNames []string
			for n, p := range ws.Projects {
				if p.Favorite {
					projNames = append(projNames, n)
				}
			}
			for n, g := range ws.Groups {
				if g.Favorite {
					groupNames = append(groupNames, n)
				}
			}
			if len(projNames)+len(groupNames) == 0 {
				fmt.Println("No favorites. Use `ws favorite add <project | @group>` to pin one.")
				return nil
			}
			sort.Strings(projNames)
			sort.Strings(groupNames)
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tCATEGORY\tGROUP")
			for _, n := range groupNames {
				fmt.Fprintf(tw, "@%s\tgroup\t-\t-\n", n)
			}
			for _, n := range projNames {
				p := ws.Projects[n]
				group := p.Group
				if group == "" {
					group = "-"
				}
				fmt.Fprintf(tw, "%s\tproject\t%s\t%s\n", n, p.Category, group)
			}
			return tw.Flush()
		},
	}
}

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
