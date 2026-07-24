package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/kuchmenko/workspace/internal/git"
	"github.com/spf13/cobra"
)

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Find git repos not registered in workspace.toml",
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "Discover git repos under standard directories that are not yet tracked in workspace.toml",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			scanDirs := []string{"personal", "work", "playground", "researches", "tools"}
			var found int

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
	_ = category
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if shouldSkipScanEntry(entry) {
			continue
		}
		entryPath := filepath.Join(absDir, entry.Name())
		if git.IsRepo(entryPath) {
			reportIfUnknownRepo(entryPath, root, knownPaths, found)
			continue
		}
		scanGroupDir(entryPath, root, knownPaths, found)
	}
	return nil
}

func scanGroupDir(groupDir, root string, knownPaths map[string]bool, found *int) {
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if shouldSkipScanEntry(entry) {
			continue
		}
		entryPath := filepath.Join(groupDir, entry.Name())
		if git.IsRepo(entryPath) {
			reportIfUnknownRepo(entryPath, root, knownPaths, found)
		}
	}
}

func shouldSkipScanEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if !entry.IsDir() || strings.HasPrefix(name, ".") {
		return true
	}
	return strings.HasSuffix(name, ".bare") || strings.Contains(name, "-wt-")
}

func reportIfUnknownRepo(repoPath, root string, knownPaths map[string]bool, found *int) {
	relPath, _ := filepath.Rel(root, repoPath)
	if knownPaths[relPath] {
		return
	}
	remote, _ := git.RemoteURL(repoPath)
	fmt.Printf("  found  %s (remote: %s)\n", relPath, remote)
	*found++
}
