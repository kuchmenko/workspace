package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Find git repos not registered in workspace.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			scanDirs := []string{"personal", "work", "playground", "researches", "tools"}
			var found int

			// Build a set of known paths
			knownPaths := make(map[string]bool)
			for _, proj := range ws.Projects {
				knownPaths[proj.Path] = true
			}

			for _, dir := range scanDirs {
				absDir := filepath.Join(wsRoot, dir)
				if _, err := os.Stat(absDir); os.IsNotExist(err) {
					continue
				}

				err := scanDir(absDir, wsRoot, dir, knownPaths, &found)
				if err != nil {
					fmt.Printf("  warn  scanning %s: %v\n", dir, err)
				}
			}

			if found == 0 {
				fmt.Println("No unregistered repos found.")
			} else {
				fmt.Printf("\n%d unregistered repo(s) found. Use 'ws add <url>' to register.\n", found)
			}
			return nil
		},
	}
}

func scanDir(absDir, root, category string, knownPaths map[string]bool, found *int) error {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		entryPath := filepath.Join(absDir, entry.Name())

		if git.IsRepo(entryPath) {
			relPath, _ := filepath.Rel(root, entryPath)
			if knownPaths[relPath] {
				continue
			}

			remote, _ := git.RemoteURL(entryPath)
			fmt.Printf("  found  %s (remote: %s)\n", relPath, remote)
			*found++
		} else {
			// Recurse one level deeper (for work/<org>/<repo> structure)
			subEntries, err := os.ReadDir(entryPath)
			if err != nil {
				continue
			}
			for _, sub := range subEntries {
				if !sub.IsDir() || strings.HasPrefix(sub.Name(), ".") {
					continue
				}
				subPath := filepath.Join(entryPath, sub.Name())
				if git.IsRepo(subPath) {
					relPath, _ := filepath.Rel(root, subPath)
					if knownPaths[relPath] {
						continue
					}
					remote, _ := git.RemoteURL(subPath)
					fmt.Printf("  found  %s (remote: %s)\n", relPath, remote)
					*found++
				}
			}
		}
	}
	return nil
}
