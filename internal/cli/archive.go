package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/archive"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/layout"
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

			// Migrated projects use a bare+worktree layout that the legacy
			// tar code does not understand. Refusing here is intentional and
			// safer than producing a half-broken archive — full worktree-aware
			// archive support is tracked as a follow-up.
			if _, err := os.Stat(layout.BarePath(absPath)); err == nil {
				return fmt.Errorf("project %q uses the worktree layout; archive of migrated projects is not yet supported", name)
			}

			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				// Not cloned locally, just update status
				proj.Status = config.StatusArchived
				ws.Projects[name] = proj
				if removed := alias.RemoveForTarget(ws, name); len(removed) > 0 {
					fmt.Printf("  aliases  removed: %v\n", removed)
				}
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
			if removed := alias.RemoveForTarget(ws, name); len(removed) > 0 {
				fmt.Printf("  aliases  removed: %v\n", removed)
			}
			if err := saveWorkspace(); err != nil {
				return err
			}

			fmt.Printf("  archived %s\n", name)
			return nil
		},
	}
}
