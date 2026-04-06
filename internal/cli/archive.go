package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/archive"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/spf13/cobra"
)

func newArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <project-name>",
		Short: "Archive a project (personal→tar+cleanup, work→remove)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			proj, exists := ws.Projects[name]
			if !exists {
				return fmt.Errorf("project %q not found in registry", name)
			}

			if proj.Status == config.StatusArchived {
				return fmt.Errorf("project %q is already archived", name)
			}

			absPath := filepath.Join(wsRoot, proj.Path)
			archiveDir := filepath.Join(wsRoot, "archive")

			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				// Not cloned locally, just update status
				proj.Status = config.StatusArchived
				ws.Projects[name] = proj
				if err := saveWorkspace(); err != nil {
					return err
				}
				fmt.Printf("  archived  %s (was not cloned locally)\n", name)
				return nil
			}

			if proj.Category == config.CategoryPersonal {
				// Clean deps first
				cleaned, err := archive.CleanDeps(absPath)
				if err != nil {
					return fmt.Errorf("cleaning deps: %w", err)
				}
				if len(cleaned) > 0 {
					fmt.Printf("  cleaned  %s: %v\n", name, cleaned)
				}

				// Tar the project
				archivePath, err := archive.TarProject(absPath, archiveDir, name)
				if err != nil {
					return err
				}
				fmt.Printf("  tar      %s → %s\n", name, archivePath)
			}

			// Remove local clone
			if err := os.RemoveAll(absPath); err != nil {
				return fmt.Errorf("removing %s: %w", absPath, err)
			}
			fmt.Printf("  removed  %s\n", absPath)

			proj.Status = config.StatusArchived
			ws.Projects[name] = proj
			if err := saveWorkspace(); err != nil {
				return err
			}

			fmt.Printf("  archived %s\n", name)
			return nil
		},
	}
}
