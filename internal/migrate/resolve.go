package migrate

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

func commitReachableFromAnyBranch(repoPath, sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	branches, err := git.Branches(repoPath)
	if err != nil {
		return false, err
	}
	for _, b := range branches {
		if err := runGit(repoPath, "merge-base", "--is-ancestor", sha, b); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func resolveDefaultBranch(name string, proj *config.Project, mainPath string, opts Options) (string, error) {
	if proj.DefaultBranch != "" {
		return proj.DefaultBranch, nil
	}
	if br := git.SymbolicRef(mainPath, "refs/remotes/origin/HEAD"); br != "" {
		if i := strings.Index(br, "/"); i >= 0 {
			br = br[i+1:]
		}
		return br, nil
	}

	var candidates []string
	for _, c := range []string{"main", "master", "trunk"} {
		if git.HasBranch(mainPath, c) {
			candidates = append(candidates, c)
		}
	}
	if opts.PromptDefaultBranch == nil {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		return "", fmt.Errorf("cannot determine default branch for %s and no prompter configured", name)
	}
	picked, err := opts.PromptDefaultBranch(name, candidates)
	if err != nil {
		return "", err
	}
	picked = strings.TrimSpace(picked)
	if picked == "" {
		return "", fmt.Errorf("no default branch selected for %s", name)
	}
	return picked, nil
}
