package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Clone missing active repos and pull existing ones",
		RunE: func(cmd *cobra.Command, args []string) error {
			var cloned, pulled, skipped, failed int

			for name, proj := range ws.Projects {
				if proj.Status != "active" {
					skipped++
					continue
				}

				absPath := filepath.Join(wsRoot, proj.Path)

				if _, err := os.Stat(absPath); os.IsNotExist(err) {
					// Clone
					fmt.Printf("  clone  %s → %s\n", name, proj.Path)
					if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
						fmt.Printf("  error  %s: %v\n", name, err)
						failed++
						continue
					}
					if err := git.Clone(proj.Remote, absPath); err != nil {
						fmt.Printf("  error  %s: %v\n", name, err)
						failed++
						continue
					}
					cloned++
				} else if git.IsRepo(absPath) {
					// Pull
					fmt.Printf("  pull   %s\n", name)
					if err := git.Pull(absPath); err != nil {
						fmt.Printf("  warn   %s: %v\n", name, err)
						// Don't count as failed — repo exists, just can't ff
					}
					pulled++
				} else {
					fmt.Printf("  skip   %s (exists but not a git repo)\n", name)
					skipped++
				}
			}

			fmt.Printf("\nDone: %d cloned, %d pulled, %d skipped, %d failed\n", cloned, pulled, skipped, failed)

			return nil
		},
	}
}
