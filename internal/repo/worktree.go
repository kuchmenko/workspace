package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type WorktreeAddOptions struct {
	WorkspaceRoot string
	Workspace     *config.Workspace
	Save          func(*config.Workspace) error
	Project       string
	Branch        string
	Machine       string
	From          string
}

type WorktreeAddResult struct {
	Path         string
	Base         string
	Machines     []string
	Source       string
	Warning      string
	ReRegistered bool
}

type WorktreeRemoveOptions struct {
	WorkspaceRoot string
	Workspace     *config.Workspace
	Save          func(*config.Workspace) error
	Project       string
	Branch        string
	Machine       string
	Force         bool
}

type WorktreeRemoveResult struct {
	Removed          bool
	MetadataReleased bool
}

func AddWorktree(options WorktreeAddOptions) (*WorktreeAddResult, error) {
	result, err := AddWorktreeCheckout(options)
	if err != nil {
		return result, err
	}
	if err := RegisterWorktree(options, result); err != nil {
		if result.ReRegistered {
			return result, fmt.Errorf("registry update failed: %w", err)
		}
		return result, fmt.Errorf("worktree created but registry save failed: %w", err)
	}
	return result, nil
}

func AddWorktreeCheckout(options WorktreeAddOptions) (*WorktreeAddResult, error) {
	branch := strings.TrimSpace(options.Branch)
	if branch == "" {
		return nil, errors.New("branch must not be empty")
	}
	if options.Machine == "" {
		return nil, errors.New("machine name is required")
	}
	if err := validateWorktreeBranch(branch); err != nil {
		return nil, err
	}
	_, project, mainPath, barePath, err := loadWorktreeProject(options.WorkspaceRoot, options.Project, options.Workspace)
	if err != nil {
		return nil, err
	}
	if !git.HasFetchRefspec(barePath) {
		_ = git.SetFetchRefspec(barePath)
	}
	_ = git.FetchRefspec(barePath, "origin", branch)
	localExists := git.HasBranch(barePath, branch)
	remoteExists := git.HasRemoteBranch(barePath, "origin", branch)
	existingPath, err := worktreeForBranch(barePath, branch)
	if err != nil {
		return nil, err
	}
	if existingPath != "" {
		return &WorktreeAddResult{Path: existingPath, ReRegistered: true, Source: addWorktreeSource(localExists, remoteExists)}, nil
	}

	result := &WorktreeAddResult{}
	result.Path = layout.WorktreePathForBranch(mainPath, options.Machine, branch)
	if _, err := os.Stat(result.Path); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", result.Path)
	}
	createFrom := ""
	switch {
	case localExists:
		result.Source = "local"
		if remoteExists {
			result.Source = "fetched"
		}
		if options.From != "" {
			result.Warning = fmt.Sprintf("--from ignored: branch %s already exists locally", branch)
		}
	case remoteExists:
		result.Source = "fetched"
		createFrom = "origin/" + branch
		if options.From != "" {
			result.Warning = fmt.Sprintf("--from ignored: branch %s already exists on origin", branch)
		}
	default:
		result.Base = options.From
		if result.Base == "" {
			result.Base = project.DefaultBranch
		}
		if result.Base == "" {
			return nil, fmt.Errorf("project %s has no default_branch and --from was not given", options.Project)
		}
		createFrom = result.Base
	}
	if err := git.WorktreeAdd(barePath, result.Path, branch, createFrom); err != nil {
		return nil, err
	}
	if result.Source == "fetched" {
		_ = git.SetBranchUpstream(result.Path, branch, "origin")
	}
	return result, nil
}

func RegisterWorktree(options WorktreeAddOptions, result *WorktreeAddResult) error {
	if options.Save == nil {
		return errors.New("workspace registry save is required")
	}
	workspace, project, _, _, err := loadWorktreeProject(options.WorkspaceRoot, options.Project, options.Workspace)
	if err != nil {
		return err
	}
	setAddMetadata(&project, strings.TrimSpace(options.Branch), options.Machine, result.Source == "fetched", result)
	workspace.Projects[options.Project] = project
	return options.Save(workspace)
}

func addWorktreeSource(localExists, remoteExists bool) string {
	if remoteExists {
		return "fetched"
	}
	if localExists {
		return "local"
	}
	return ""
}

func RemoveWorktree(options WorktreeRemoveOptions) (WorktreeRemoveResult, error) {
	result := WorktreeRemoveResult{}
	if strings.TrimSpace(options.Branch) == "" {
		return result, errors.New("branch must not be empty")
	}
	if options.Machine == "" {
		return result, errors.New("machine name is required")
	}
	workspace, project, mainPath, barePath, err := loadWorktreeProject(options.WorkspaceRoot, options.Project, options.Workspace)
	if err != nil {
		return result, err
	}
	wtPath, err := worktreeForBranch(barePath, options.Branch)
	if err != nil {
		return result, err
	}
	if wtPath == "" {
		meta := project.LookupBranch(options.Branch)
		if meta == nil || !slices.Contains(meta.Machines, options.Machine) {
			return result, fmt.Errorf("no worktree on branch %s in project %s", options.Branch, options.Project)
		}
	} else {
		if wtPath == mainPath {
			return result, fmt.Errorf("refusing to remove main worktree of %s (branch %s is checked out at %s)", options.Project, options.Branch, mainPath)
		}
		if !options.Force {
			if git.IsDirty(wtPath) {
				return result, fmt.Errorf("worktree %s is dirty; commit/stash or use --force", wtPath)
			}
			ahead, _, has := git.AheadBehind(wtPath, options.Branch)
			if has && ahead > 0 {
				return result, fmt.Errorf("branch %s has %d unpushed commits; push or use --force", options.Branch, ahead)
			}
		}
		if err := git.WorktreeRemove(barePath, wtPath, options.Force); err != nil {
			return result, err
		}
		result.Removed = true
	}
	if changed, _ := project.ReleaseBranch(options.Branch, options.Machine); changed {
		workspace.Projects[options.Project] = project
		if options.Save == nil {
			return result, errors.New("workspace registry save is required")
		}
		saveErr := options.Save(workspace)
		if saveErr != nil {
			if result.Removed {
				return result, fmt.Errorf("worktree removed but registry ownership release failed: %w", saveErr)
			}
			return result, fmt.Errorf("registry ownership release failed: %w", saveErr)
		}
		result.MetadataReleased = true
	}
	return result, nil
}

func loadWorktreeProject(root, name string, workspace *config.Workspace) (*config.Workspace, config.Project, string, string, error) {
	if workspace == nil {
		return nil, config.Project{}, "", "", errors.New("workspace registry is required")
	}
	project, ok := workspace.Projects[name]
	if !ok {
		return nil, config.Project{}, "", "", fmt.Errorf("project %q not found in workspace registry", name)
	}
	mainPath, err := layout.ProjectPath(root, project.Path)
	if err != nil {
		return nil, config.Project{}, "", "", fmt.Errorf("project %q: %w", name, err)
	}
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		return nil, config.Project{}, "", "", fmt.Errorf("project %q is not migrated yet (no %s); run `ws migrate %s`", name, filepath.Base(barePath), name)
	}
	return workspace, project, mainPath, barePath, nil
}

func validateWorktreeBranch(branch string) error {
	out, err := exec.Command("git", "check-ref-format", "--branch", branch).CombinedOutput()
	if err == nil {
		return nil
	}
	hint := strings.TrimSpace(string(out))
	if hint == "" {
		hint = "git check-ref-format rejected this name"
	}
	return fmt.Errorf("invalid branch name %q: %s", branch, hint)
}

func worktreeForBranch(barePath, branch string) (string, error) {
	worktrees, err := git.WorktreeList(barePath)
	if err != nil {
		return "", fmt.Errorf("list worktrees: %w", err)
	}
	for _, worktree := range worktrees {
		if !worktree.Bare && worktree.Branch == branch {
			return worktree.Path, nil
		}
	}
	return "", nil
}

func setAddMetadata(project *config.Project, branch, machine string, remote bool, result *WorktreeAddResult) {
	project.ClaimBranch(branch, machine)
	if remote {
		project.MarkPushed(branch, machine, time.Now())
	}
	result.Machines = append([]string(nil), project.LookupBranch(branch).Machines...)
}
