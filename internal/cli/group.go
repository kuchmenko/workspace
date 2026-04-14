package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/spf13/cobra"
)

func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage project groups",
		Annotations: map[string]string{
			"capability": "organisation",
			"agent:when": "Create and inspect project groups for organizing repos by team or domain",
		},
	}

	cmd.AddCommand(
		newGroupAddCmd(),
		newGroupListCmd(),
		newGroupShowCmd(),
	)

	return cmd
}

func newGroupAddCmd() *cobra.Command {
	var description string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new group",
		Annotations: map[string]string{
			"capability": "organisation",
			"agent:when": "Create a new project group and its directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, exists := ws.Groups[name]; exists {
				return fmt.Errorf("group %q already exists", name)
			}

			ws.Groups[name] = config.Group{
				Description: description,
			}

			// Create the directory
			groupDir := filepath.Join(wsRoot, name)
			if err := os.MkdirAll(groupDir, 0o755); err != nil {
				return err
			}

			if err := saveWorkspace(); err != nil {
				return err
			}

			fmt.Printf("  created group %q (%s)\n", name, description)
			return nil
		},
	}

	cmd.Flags().StringVarP(&description, "desc", "d", "", "group description")
	return cmd
}

func newGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all groups with project counts",
		Annotations: map[string]string{
			"capability": "organisation",
			"agent:when": "List all defined groups with their project counts",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Groups) == 0 {
				fmt.Println("No groups defined. Use 'ws group add <name>' to create one.")
				return nil
			}

			// Count projects per group
			counts := make(map[string]int)
			for _, proj := range ws.Projects {
				if proj.Group != "" {
					counts[proj.Group]++
					// Also count for parent groups
					parts := strings.Split(proj.Group, "/")
					for i := range len(parts) - 1 {
						parent := strings.Join(parts[:i+1], "/")
						counts[parent]++
					}
				}
			}

			names := make([]string, 0, len(ws.Groups))
			for name := range ws.Groups {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				g := ws.Groups[name]
				desc := g.Description
				if desc == "" {
					desc = "-"
				}
				fmt.Printf("  %-20s  %d projects  %s\n", name, counts[name], desc)
			}
			return nil
		},
	}
}

func newGroupShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show projects in a group",
		Annotations: map[string]string{
			"capability": "organisation",
			"agent:when": "Show all projects belonging to a specific group",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupName := args[0]

			if _, exists := ws.Groups[groupName]; !exists {
				return fmt.Errorf("group %q not found", groupName)
			}

			projects := projectsInGroup(groupName)
			if len(projects) == 0 {
				fmt.Printf("Group %q has no projects.\n", groupName)
				return nil
			}

			fmt.Printf("Group: %s\n", groupName)
			if desc := ws.Groups[groupName].Description; desc != "" {
				fmt.Printf("  %s\n", desc)
			}
			fmt.Println()

			for _, name := range projects {
				proj := ws.Projects[name]
				fmt.Printf("  %-20s  %s  %s  %s\n", name, proj.Status, proj.Path, proj.Remote)
			}
			return nil
		},
	}
}

// projectsInGroup returns sorted project names belonging to a group (including subgroups).
func projectsInGroup(groupName string) []string {
	var names []string
	prefix := groupName + "/"
	for name, proj := range ws.Projects {
		if proj.Group == groupName || strings.HasPrefix(proj.Group, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
