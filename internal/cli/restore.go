package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/archive"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <project-name>",
		Short: "Restore an archived project (untar or re-clone)",
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "Restore an archived project from tarball or by re-cloning from remote",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			proj, exists := ws.Projects[name]
			if !exists {
				return fmt.Errorf("project %q not found in registry", name)
			}

			if proj.Status == config.StatusActive {
				return fmt.Errorf("project %q is already active", name)
			}

			absPath := filepath.Join(wsRoot, proj.Path)
			archiveDir := filepath.Join(wsRoot, "archive")

			// Check if already exists locally
			if _, err := os.Stat(absPath); err == nil {
				fmt.Printf("  exists   %s (already on disk)\n", name)
				proj.Status = config.StatusActive
				ws.Projects[name] = proj
				return saveWorkspace()
			}

			// Try to restore from archive first (personal projects)
			if proj.Category == config.CategoryPersonal && archive.ArchiveExists(archiveDir, name) {
				destDir := filepath.Dir(absPath)
				fmt.Printf("  untar    %s\n", name)
				if err := archive.UntarProject(filepath.Join(archiveDir, name+".tar.gz"), destDir); err != nil {
					return err
				}
				// Remove the archive after successful restore
				os.Remove(filepath.Join(archiveDir, name+".tar.gz"))
			} else {
				// Clone from remote
				fmt.Printf("  clone    %s → %s\n", name, proj.Path)
				if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
					return err
				}
				if err := git.Clone(proj.Remote, absPath); err != nil {
					return err
				}
			}

			proj.Status = config.StatusActive
			ws.Projects[name] = proj
			if err := saveWorkspace(); err != nil {
				return err
			}

			fmt.Printf("  restored %s\n", name)
			return nil
		},
	}
}
