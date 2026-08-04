package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/tui"
	"github.com/spf13/cobra"
)

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage shell aliases for projects and groups",
		Args:  cobra.NoArgs,
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
	m := alias.NewManagerModel(ws, wsRoot)
	p := tui.NewProgram(m, tui.WithAltScreen())
	res, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI crashed: %w", err)
	}
	final := res.(alias.ManagerModel)
	r := final.GetResult()
	if r.Canceled || !r.Confirmed {
		fmt.Println("Aliases unchanged.")
		return nil
	}
	ws.Aliases = r.Aliases
	if err := saveWorkspace(); err != nil {
		return err
	}
	metrics.RecordAliasManaged()
	fmt.Printf("Saved %d aliases.\n", len(ws.Aliases))
	return nil
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List configured aliases",
		Annotations: agentAnnotations("alias-list", AgentInteractionNone, AgentApprovalNone, AgentEffectNone, AgentEffectNone, "table", "0,1"),
		Args:        cobra.NoArgs,
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
		Use:         "add <alias> <target>",
		Short:       "Add an alias for a project or group",
		Annotations: agentAnnotations("alias-add", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "text", "0,1"),
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, target := args[0], args[1]
			if err := alias.ValidateName(name); err != nil {
				return err
			}
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
			for existingName, existingTarget := range ws.Aliases {
				if existingName != name && existingTarget == target && !force {
					return fmt.Errorf("target %q already has alias %q; use --force to replace it", target, existingName)
				}
			}
			if path, conflict := alias.ShellConflict(name); conflict && !force {
				return fmt.Errorf("alias %q would shadow existing command at %s; use --force to override", name, path)
			}
			if ws.Aliases == nil {
				ws.Aliases = make(map[string]string)
			}
			if force {
				for existingName, existingTarget := range ws.Aliases {
					if existingName != name && existingTarget == target {
						delete(ws.Aliases, existingName)
					}
				}
			}
			ws.Aliases[name] = target
			if err := saveWorkspace(); err != nil {
				return err
			}
			metrics.RecordAliasManaged()
			fmt.Printf("Added alias %s → %s\n", name, target)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite existing alias or shadow existing command")
	return c
}

func newAliasRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "rm <alias>",
		Short:       "Remove an alias",
		Annotations: agentAnnotations("alias-remove", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "text", "0,1"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := ws.Aliases[name]; !ok {
				return fmt.Errorf("alias %q not defined", name)
			}
			delete(ws.Aliases, name)
			if err := saveWorkspace(); err != nil {
				return err
			}
			metrics.RecordAliasManaged()
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
			metrics.RecordAliasStateGenerated()
			return nil
		},
	}
}

func newAliasInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "install",
		Short:       "Add a sourcing line to ~/.zshrc (idempotent)",
		Annotations: agentAnnotations("alias-install", AgentInteractionNone, AgentApprovalRequired, AgentEffectWrite, AgentEffectNone, "text", "0,1"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := alias.WriteStateFile(ws, wsRoot); err != nil {
				return err
			}
			metrics.RecordAliasStateGenerated()
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
