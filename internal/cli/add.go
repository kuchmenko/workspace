package cli

import (
	"fmt"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		category string
		group    string
		name     string
		noClone  bool
	)

	cmd := &cobra.Command{
		Use:   "add <remote-url>",
		Short: "Register and clone a new project",
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "Register a new git repository in workspace.toml and clone it locally",
			"agent:safety": "Creates a new directory and updates workspace.toml. Use --no-clone to register without cloning.",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remote := args[0]

			if name == "" {
				name = git.ParseRepoName(remote)
			}

			if _, exists := ws.Projects[name]; exists {
				return fmt.Errorf("project %q already registered", name)
			}

			cat := config.Category(category)
			if cat != config.CategoryPersonal && cat != config.CategoryWork {
				return fmt.Errorf("category must be 'personal' or 'work', got %q", category)
			}

			// Build path: <group>/<repo-name> or <category>/<repo-name>
			var relPath string
			if group != "" {
				relPath = filepath.Join(group, name)
			} else {
				relPath = filepath.Join(string(cat), name)
			}

			proj := config.Project{
				Remote:   remote,
				Path:     relPath,
				Status:   config.StatusActive,
				Category: cat,
				Group:    group,
			}

			if !noClone {
				absPath := filepath.Join(wsRoot, relPath)
				fmt.Printf("  clone  %s → %s\n", name, relPath)
				if err := git.Clone(remote, absPath); err != nil {
					return err
				}
			}

			ws.Projects[name] = proj
			if err := saveWorkspace(); err != nil {
				return err
			}

			fmt.Printf("  added  %s (group: %s, %s)\n", name, groupOrDefault(group, cat), proj.Status)
			return nil
		},
	}

	cmd.Flags().StringVarP(&category, "category", "c", "personal", "project category: personal or work")
	cmd.Flags().StringVarP(&group, "group", "g", "", "group/directory for the project (e.g. limitless, personal/tools)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "project name (default: derived from URL)")
	cmd.Flags().BoolVar(&noClone, "no-clone", false, "register without cloning")

	return cmd
}

func groupOrDefault(group string, cat config.Category) string {
	if group != "" {
		return group
	}
	return string(cat)
}
