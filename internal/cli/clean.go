package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kuchmenko/workspace/internal/archive"
	"github.com/spf13/cobra"
)

func newCleanCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "clean [project-name]",
		Short: "Remove dependency/build caches (node_modules, target/, .venv, etc.)",
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "Free disk space by removing build artifacts and dependency caches from a project",
			"agent:safety": "Deletes node_modules, target/, .venv, dist/, .next/, .svelte-kit/ etc. Recoverable via reinstall.",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return cleanProject(args[0])
			}
			if all {
				return cleanAll()
			}
			return fmt.Errorf("specify a project name or use --all")
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "clean all active projects")
	return cmd
}

func cleanProject(name string) error {
	proj, exists := ws.Projects[name]
	if !exists {
		return fmt.Errorf("project %q not found in registry", name)
	}

	absPath := filepath.Join(wsRoot, proj.Path)
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("project %q not cloned locally", name)
	}

	cleaned, err := archive.CleanDeps(absPath)
	if err != nil {
		return err
	}

	if len(cleaned) == 0 {
		fmt.Printf("  %s: nothing to clean\n", name)
	} else {
		fmt.Printf("  %s: removed %v\n", name, cleaned)
	}
	return nil
}

func cleanAll() error {
	names := make([]string, 0, len(ws.Projects))
	for name := range ws.Projects {
		names = append(names, name)
	}
	sort.Strings(names)

	var totalCleaned int
	for _, name := range names {
		proj := ws.Projects[name]
		if proj.Status != "active" {
			continue
		}
		absPath := filepath.Join(wsRoot, proj.Path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			continue
		}
		cleaned, err := archive.CleanDeps(absPath)
		if err != nil {
			fmt.Printf("  %s: error: %v\n", name, err)
			continue
		}
		if len(cleaned) > 0 {
			fmt.Printf("  %s: removed %v\n", name, cleaned)
			totalCleaned += len(cleaned)
		}
	}

	if totalCleaned == 0 {
		fmt.Println("Nothing to clean.")
	}
	return nil
}
