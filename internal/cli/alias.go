package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/aliasmgr"
	"github.com/spf13/cobra"
)

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage shell aliases for projects and groups",
		RunE:  runAliasTUI,
	}
	cmd.AddCommand(
		newAliasListCmd(),
		newAliasAddCmd(),
		newAliasRmCmd(),
		newAliasInitCmd(),
		newAliasInstallCmd(),
	)
	return cmd
}

func runAliasTUI(cmd *cobra.Command, args []string) error {
	m := aliasmgr.New(ws, wsRoot)
	p := tea.NewProgram(m, tea.WithAltScreen())
	res, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI crashed: %w", err)
	}
	final := res.(aliasmgr.Model)
	r := final.GetResult()
	if r.Cancelled || !r.Confirmed {
		fmt.Println("Aliases unchanged.")
		return nil
	}
	ws.Aliases = r.Aliases
	if err := saveWorkspace(); err != nil {
		return err
	}
	fmt.Printf("Saved %d aliases.\n", len(ws.Aliases))
	return nil
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured aliases",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Aliases) == 0 {
				fmt.Println("No aliases defined. Run `ws alias` to create some.")
				return nil
			}
			resolved := alias.ResolveAll(ws, wsRoot)
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ALIAS\tTARGET\tKIND\tPATH")
			for _, r := range resolved {
				kind := r.Kind.String()
				path := r.Path
				if r.Kind == alias.TargetUnknown {
					path = "(broken)"
					kind = "?"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Target, kind, path)
			}
			return tw.Flush()
		},
	}
}

func newAliasAddCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "add <alias> <target>",
		Short: "Add an alias for a project or group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, target := args[0], args[1]
			if target != alias.RootTarget {
				if _, ok := ws.Projects[target]; !ok {
					if _, ok := ws.Groups[target]; !ok {
						return fmt.Errorf("target %q is not a known project or group (use %q for workspace root)", target, alias.RootTarget)
					}
				}
			}
			if existing, ok := ws.Aliases[name]; ok && !force {
				return fmt.Errorf("alias %q already exists (→ %s); use --force to overwrite", name, existing)
			}
			if path, conflict := alias.ShellConflict(name); conflict && !force {
				return fmt.Errorf("alias %q would shadow existing command at %s; use --force to override", name, path)
			}
			if ws.Aliases == nil {
				ws.Aliases = make(map[string]string)
			}
			ws.Aliases[name] = target
			if err := saveWorkspace(); err != nil {
				return err
			}
			fmt.Printf("Added alias %s → %s\n", name, target)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite existing alias or shadow existing command")
	return c
}

func newAliasRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove an alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := ws.Aliases[name]; !ok {
				return fmt.Errorf("alias %q not defined", name)
			}
			delete(ws.Aliases, name)
			if err := saveWorkspace(); err != nil {
				return err
			}
			fmt.Printf("Removed alias %s\n", name)
			return nil
		},
	}
}

func newAliasInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [shell]",
		Short: "Print shell snippet to eval (default: zsh)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := "zsh"
			if len(args) == 1 {
				shell = args[0]
			}
			if shell != "zsh" {
				return fmt.Errorf("shell %q not supported (only zsh for now)", shell)
			}
			resolved := alias.ResolveAll(ws, wsRoot)
			fmt.Print(alias.RenderZsh(resolved))
			return nil
		},
	}
}

func newAliasInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Add a sourcing line to ~/.zshrc (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Make sure state file exists before installing the source line.
			if err := alias.WriteStateFile(ws, wsRoot); err != nil {
				return err
			}
			added, rc, err := alias.InstallZshrc()
			if err != nil {
				return err
			}
			path, _ := alias.StateFilePath()
			if !added {
				fmt.Printf("Already installed in %s\n", rc)
				fmt.Printf("Aliases sourced from %s\n", path)
				return nil
			}
			fmt.Printf("Installed sourcing block in %s\n", rc)
			fmt.Printf("Aliases will be loaded from %s\n", path)
			fmt.Println("Open a new shell or run: source ~/.zshrc")
			return nil
		},
	}
}

// sortedAliasNames is a small helper used by tests / debug paths.
func sortedAliasNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
