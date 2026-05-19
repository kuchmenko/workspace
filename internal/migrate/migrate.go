// Package migrate converts plain `git clone` checkouts under a workspace
// into the worktree-based layout (bare repo + main worktree sibling).
//
// The migration is intentionally fail-safe rather than reversible: there is
// no `ws unmigrate`, but every step before the irreversible final swap
// preserves the original .git so the user can recover by hand. See
// MigrateProject for the precise ordering.
package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type Options struct {
	WIP bool

	StashBranch bool

	CheckoutDefault bool

	Machine string

	PromptDefaultBranch func(project string, candidates []string) (string, error)

	Logf func(format string, args ...interface{})
}

func (o Options) logf(format string, args ...interface{}) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

type Result struct {
	Project        string
	BarePath       string
	MainWorktree   string
	DefaultBranch  string
	HooksMigrated  []string
	WIPBranch      string
	WIPWorktree    string
	StashBranches  []string
	DetachedBranch string
	BranchesPushed int
}

var ErrAlreadyMigrated = errors.New("project already migrated")

func MigrateProject(wsRoot string, name string, proj *config.Project, opts Options) (*Result, error) {
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	if _, err := os.Stat(barePath); err == nil {
		return nil, ErrAlreadyMigrated
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("project path %s does not exist", mainPath)
	}
	if !git.IsRepo(mainPath) {
		return nil, fmt.Errorf("%s is not a git repo", mainPath)
	}
	if git.IsBare(mainPath) {
		return nil, fmt.Errorf("%s is already a bare repo (unexpected layout)", mainPath)
	}

	opts.logf("migrate %s: starting at %s", name, mainPath)

	defaultBranch, err := resolveDefaultBranch(name, proj, mainPath, opts)
	if err != nil {
		return nil, err
	}
	opts.logf("migrate %s: default branch = %s", name, defaultBranch)

	ts := time.Now().Unix()

	originalBranch, _ := git.CurrentBranch(mainPath)
	detachedBranch := ""
	if originalBranch == "" {
		if !opts.CheckoutDefault {
			return nil, fmt.Errorf("%s is in detached HEAD; check out a branch first or re-run with the interactive TUI", name)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("detached-HEAD recovery requires a configured machine name")
		}
		head := git.RevParse(mainPath, "HEAD")

		reachable, _ := commitReachableFromAnyBranch(mainPath, head)
		if !reachable {
			topic := fmt.Sprintf("migration-detached-%d", ts)
			detachedBranch = layout.BranchName(opts.Machine, topic)
			opts.logf("migrate %s: detached HEAD at %s → preserving as %s", name, head, detachedBranch)
			if err := runGit(mainPath, "branch", detachedBranch); err != nil {
				return nil, fmt.Errorf("preserve detached HEAD as branch %s: %w", detachedBranch, err)
			}
		} else {
			opts.logf("migrate %s: detached HEAD at %s — already reachable from a branch, no preservation needed", name, head)
		}
		opts.logf("migrate %s: checking out %s", name, defaultBranch)
		if err := runGit(mainPath, "checkout", defaultBranch); err != nil {
			return nil, fmt.Errorf("checkout %s from detached HEAD: %w", defaultBranch, err)
		}
		originalBranch = defaultBranch
	}

	stashCount := git.StashCount(mainPath)
	stashBranches := []string{}
	if stashCount > 0 {
		if !opts.StashBranch {
			return nil, fmt.Errorf("%s has %d stash entries; re-run with stash-branch enabled (TUI: pick `branch`) to convert each into a wt/<machine>/migration-stash-<ts>-N branch", name, stashCount)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("stash-to-branch requires a configured machine name")
		}

		for i := 0; i < stashCount; i++ {
			topic := fmt.Sprintf("migration-stash-%d-%d", ts, i)
			br := layout.BranchName(opts.Machine, topic)
			opts.logf("migrate %s: converting stash@{0} → %s", name, br)
			if err := runGit(mainPath, "stash", "branch", br); err != nil {
				return nil, fmt.Errorf("stash branch %s: %w", br, err)
			}

			if err := runGit(mainPath, "add", "-A"); err != nil {
				return nil, fmt.Errorf("stage stash branch %s: %w", br, err)
			}
			if err := runGit(mainPath, "commit", "-m", fmt.Sprintf("ws: migration stash@{0} snapshot (%d)", i)); err != nil {
				return nil, fmt.Errorf("commit stash branch %s: %w", br, err)
			}
			stashBranches = append(stashBranches, br)

			if err := runGit(mainPath, "checkout", originalBranch); err != nil {
				return nil, fmt.Errorf("restore %s after stash branch: %w", originalBranch, err)
			}
		}
	}

	dirty := git.IsDirty(mainPath)
	wipBranch := ""
	wipTopic := ""
	if dirty {
		if !opts.WIP {
			return nil, fmt.Errorf("%s has uncommitted changes; commit them or re-run with --wip to snapshot to a wt/<machine>/migration-wip branch", name)
		}
		if opts.Machine == "" {
			return nil, fmt.Errorf("--wip requires a configured machine name")
		}
		wipTopic = fmt.Sprintf("migration-wip-%d", ts)
		wipBranch = layout.BranchName(opts.Machine, wipTopic)
		opts.logf("migrate %s: dirty tree → snapshot to %s", name, wipBranch)
		if err := runGit(mainPath, "checkout", "-b", wipBranch); err != nil {
			return nil, fmt.Errorf("create WIP branch: %w", err)
		}
		if err := runGit(mainPath, "add", "-A"); err != nil {
			return nil, fmt.Errorf("stage WIP: %w", err)
		}
		if err := runGit(mainPath, "commit", "-m", "ws: migration WIP snapshot"); err != nil {
			return nil, fmt.Errorf("commit WIP: %w", err)
		}

		if err := runGit(mainPath, "checkout", originalBranch); err != nil {
			return nil, fmt.Errorf("restore original branch %s: %w", originalBranch, err)
		}
	}

	currentBranch := originalBranch
	localBranches, err := git.Branches(mainPath)
	if err != nil {
		return nil, fmt.Errorf("list local branches: %w", err)
	}
	originalHead := git.RevParse(mainPath, "HEAD")
	if originalHead == "" {
		return nil, fmt.Errorf("could not resolve HEAD in %s", mainPath)
	}

	hooksDir := filepath.Join(mainPath, ".git", "hooks")
	activeHooks, _ := listActiveHooks(hooksDir)
	if len(activeHooks) > 0 {
		opts.logf("migrate %s: found %d active hook(s): %s", name, len(activeHooks), strings.Join(activeHooks, ", "))
	}

	opts.logf("migrate %s: cloning bare → %s", name, barePath)
	if err := git.CloneBareLocal(mainPath, barePath); err != nil {
		return nil, err
	}

	for _, b := range localBranches {
		if git.HasBranch(barePath, b) {
			continue
		}
		opts.logf("migrate %s: backfilling missing branch %s into bare", name, b)
		if err := git.FetchRefspec(barePath, mainPath, b+":refs/heads/"+b); err != nil {
			rollbackBare(barePath)
			return nil, fmt.Errorf("backfill branch %s: %w", b, err)
		}
	}

	if proj.Remote != "" {
		if err := git.SetRemoteURL(barePath, proj.Remote); err != nil {
			rollbackBare(barePath)
			return nil, err
		}
		if err := git.SetFetchRefspec(barePath); err != nil {
			rollbackBare(barePath)
			return nil, fmt.Errorf("set fetch refspec: %w", err)
		}
		if err := git.Fetch(barePath); err != nil {
			opts.logf("migrate %s: warning: initial fetch failed: %v", name, err)
		}

		_ = git.SetRemoteHead(barePath, defaultBranch)
	}

	if err := git.SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("migrate %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}

	migratedHooks, err := copyHooks(hooksDir, filepath.Join(barePath, "hooks"), activeHooks)
	if err != nil {
		opts.logf("migrate %s: warning: hook migration partial: %v", name, err)
	}

	movedGit := filepath.Join(mainPath, fmt.Sprintf(".git.migrating-%d", ts))
	if err := os.Rename(filepath.Join(mainPath, ".git"), movedGit); err != nil {
		rollbackBare(barePath)
		return nil, fmt.Errorf("move .git aside: %w", err)
	}

	restore := func() {
		_ = os.Rename(movedGit, filepath.Join(mainPath, ".git"))
		rollbackBare(barePath)
	}

	tmpParent := filepath.Join(filepath.Dir(mainPath), fmt.Sprintf(".ws-migrate-%d", ts))
	tmpWT := filepath.Join(tmpParent, filepath.Base(mainPath))

	_ = os.RemoveAll(tmpParent)
	if err := os.MkdirAll(tmpParent, 0o755); err != nil {
		restore()
		return nil, fmt.Errorf("create tmp parent: %w", err)
	}

	if err := git.WorktreeAddNoCheckout(barePath, tmpWT, currentBranch); err != nil {
		_ = os.RemoveAll(tmpParent)
		restore()
		return nil, fmt.Errorf("create tmp worktree: %w", err)
	}

	tmpDotGit := filepath.Join(tmpWT, ".git")
	if err := os.Rename(tmpDotGit, filepath.Join(mainPath, ".git")); err != nil {
		_ = os.RemoveAll(tmpParent)
		restore()
		return nil, fmt.Errorf("move .git pointer from tmp: %w", err)
	}

	if err := os.RemoveAll(tmpParent); err != nil {
		opts.logf("migrate %s: warning: could not remove %s: %v", name, tmpParent, err)
	}

	if err := git.WorktreeRepair(mainPath); err != nil {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("worktree repair: %w", err)
	}

	if err := runGit(mainPath, "reset", "--mixed", "HEAD"); err != nil {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("populate index from HEAD: %w", err)
	}

	if !git.IsRepo(mainPath) {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("worktree verification failed: %s is no longer a git repo", mainPath)
	}
	if newHead := git.RevParse(mainPath, "HEAD"); newHead != originalHead {
		_ = os.RemoveAll(filepath.Join(mainPath, ".git"))
		restore()
		return nil, fmt.Errorf("worktree verification failed: HEAD shifted from %s to %s", originalHead, newHead)
	}

	if err := os.RemoveAll(movedGit); err != nil {
		opts.logf("migrate %s: warning: could not remove %s: %v", name, movedGit, err)
	}

	wipWorktree := ""
	if wipBranch != "" {
		wipWorktree = layout.WorktreePath(mainPath, opts.Machine, wipTopic)
		if err := git.WorktreeAdd(barePath, wipWorktree, wipBranch, ""); err != nil {
			opts.logf("migrate %s: warning: could not create WIP worktree: %v", name, err)
			wipWorktree = ""
		}
	}

	proj.DefaultBranch = defaultBranch

	opts.logf("migrate %s: done", name)

	return &Result{
		Project:        name,
		BarePath:       barePath,
		MainWorktree:   mainPath,
		DefaultBranch:  defaultBranch,
		HooksMigrated:  migratedHooks,
		WIPBranch:      wipBranch,
		WIPWorktree:    wipWorktree,
		StashBranches:  stashBranches,
		DetachedBranch: detachedBranch,
		BranchesPushed: len(localBranches),
	}, nil
}

func rollbackBare(barePath string) {
	_ = os.RemoveAll(barePath)
}
