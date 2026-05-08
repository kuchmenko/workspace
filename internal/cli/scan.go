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
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "Discover git repos under standard directories that are not yet tracked in workspace.toml",
		},
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

// scanDir walks `absDir` two levels deep looking for git repositories
// not registered in workspace.toml. The two-level depth handles the
// <category>/<project> shape and the <category>/<group>/<project>
// shape uniformly: at each entry, if it is itself a repo we report
// it; otherwise we descend one more level and report inside.
//
// Entries beginning with "." or matching the worktree-layout siblings
// (`*.bare`, `*-wt-*`) are silently skipped at every level — those
// are bookkeeping siblings of already-registered projects, not
// orphans.
func scanDir(absDir, root, category string, knownPaths map[string]bool, found *int) error {
	_ = category // reserved for future filtering
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

// scanGroupDir walks one level inside a non-repo entry (typical
// <category>/<group>/ shape used for organization-grouped repos).
// Errors reading the dir are non-fatal — scan is best-effort.
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

// shouldSkipScanEntry encapsulates the "this directory is not a
// scan candidate" rules applied at every level. Skips dotfiles,
// non-directories, and the bare+worktree bookkeeping siblings of
// registered projects.
func shouldSkipScanEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if !entry.IsDir() || strings.HasPrefix(name, ".") {
		return true
	}
	return strings.HasSuffix(name, ".bare") || strings.Contains(name, "-wt-")
}

// reportIfUnknownRepo prints one "found" line for `repoPath` if its
// workspace-relative path is not already in `knownPaths`. Increments
// `found` for the caller's tally.
func reportIfUnknownRepo(repoPath, root string, knownPaths map[string]bool, found *int) {
	relPath, _ := filepath.Rel(root, repoPath)
	if knownPaths[relPath] {
		return
	}
	remote, _ := git.RemoteURL(repoPath)
	fmt.Printf("  found  %s (remote: %s)\n", relPath, remote)
	*found++
}
