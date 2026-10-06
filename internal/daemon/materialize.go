package daemon

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/kuchmenko/workspace/internal/repo"
)

const DefaultGitSyncInterval = 15 * time.Minute

type materializeRequest struct {
	workspaceID string
	project     string
}

type materializer struct {
	store    *registry.Store
	machine  string
	interval time.Duration
	logf     func(string, ...any)
	requests chan materializeRequest
}

func newMaterializer(options Options) *materializer {
	interval := options.GitSyncInterval
	if interval <= 0 {
		interval = DefaultGitSyncInterval
	}
	return &materializer{
		store:    options.Store,
		machine:  options.Name,
		interval: interval,
		logf:     options.Logf,
		requests: make(chan materializeRequest, 128),
	}
}

func (materializer *materializer) trigger(workspaceID, project string) {
	select {
	case materializer.requests <- materializeRequest{workspaceID: workspaceID, project: project}:
	default:
	}
}

func (materializer *materializer) triggerAll() {
	materializer.trigger("", "")
}

func (materializer *materializer) run(ctx context.Context) {
	periodic := time.NewTicker(materializer.interval)
	defer periodic.Stop()
	pending := map[string]map[string]bool{}
	all := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-periodic.C:
			materializer.materialize(ctx, true, nil)
		case request := <-materializer.requests:
			if request.workspaceID == "" {
				all = true
			} else {
				projects := pending[request.workspaceID]
				if projects == nil {
					projects = map[string]bool{}
					pending[request.workspaceID] = projects
				}
				projects[request.project] = true
			}
			drainMaterializeRequests(materializer.requests, pending, &all)
			materializer.materialize(ctx, all, pending)
			all = false
			pending = map[string]map[string]bool{}
		}
	}
}

func drainMaterializeRequests(requests <-chan materializeRequest, pending map[string]map[string]bool, all *bool) {
	for {
		select {
		case request := <-requests:
			if request.workspaceID == "" {
				*all = true
				continue
			}
			projects := pending[request.workspaceID]
			if projects == nil {
				projects = map[string]bool{}
				pending[request.workspaceID] = projects
			}
			projects[request.project] = true
		default:
			return
		}
	}
}

func (materializer *materializer) materialize(ctx context.Context, all bool, pending map[string]map[string]bool) {
	workspaces, err := materializer.loadWorkspaces(ctx, all, pending)
	if err != nil {
		materializer.logf("daemon: load workspaces for Git sync: %v", err)
		return
	}
	for _, workspace := range workspaces {
		projects := pending[workspace.WorkspaceID]
		materializer.materializeWorkspace(ctx, workspace, projects)
	}
}

func (materializer *materializer) loadWorkspaces(ctx context.Context, all bool, pending map[string]map[string]bool) ([]registry.Workspace, error) {
	if all {
		return materializer.store.List(ctx)
	}
	workspaces := make([]registry.Workspace, 0, len(pending))
	for workspaceID := range pending {
		name, err := materializer.store.WorkspaceNameByID(ctx, workspaceID)
		if err != nil {
			continue
		}
		workspace, err := materializer.store.LoadByName(ctx, name)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	slices.SortFunc(workspaces, func(left, right registry.Workspace) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return workspaces, nil
}

func (materializer *materializer) materializeWorkspace(ctx context.Context, workspace registry.Workspace, selected map[string]bool) {
	changed := false
	for _, name := range slices.Sorted(maps.Keys(workspace.State.Projects)) {
		if err := ctx.Err(); err != nil {
			return
		}
		project := workspace.State.Projects[name]
		if project.Status != config.StatusActive || !projectSelected(selected, name) {
			continue
		}
		updated, err := materializer.materializeProject(ctx, workspace.Root, name, project)
		if err != nil {
			if !errors.Is(err, repo.ErrProjectLocked) {
				materializer.logf("daemon: Git sync %s/%s: %v", workspace.Name, name, err)
			}
			continue
		}
		if updated != nil {
			workspace.State.Projects[name] = *updated
			changed = true
		}
	}
	if !changed {
		return
	}
	if _, err := materializer.store.Update(ctx, workspace.Name, workspace.Revision, workspace.State); err != nil {
		materializer.logf("daemon: save Git metadata for %s: %v", workspace.Name, err)
	}
}

func projectSelected(selected map[string]bool, name string) bool {
	if len(selected) == 0 || selected[""] {
		return true
	}
	return selected[name]
}

func (materializer *materializer) materializeProject(ctx context.Context, root, name string, project config.Project) (*config.Project, error) {
	mainPath, err := layout.ProjectPath(root, project.Path)
	if err != nil {
		return nil, err
	}
	lock, err := repo.AcquireProjectLock(mainPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	mainPath, barePath, metadataChanged, err := materializer.prepareRepository(ctx, root, name, mainPath, &project)
	if err != nil {
		return nil, err
	}
	changed, err := materializer.materializeBranches(ctx, mainPath, barePath, &project)
	if err != nil {
		return nil, err
	}
	if err = materializer.fastForwardWorktrees(ctx, barePath); err != nil {
		return nil, err
	}
	if metadataChanged || changed {
		return &project, nil
	}
	return nil, nil
}

func (materializer *materializer) prepareRepository(ctx context.Context, root, name, mainPath string, project *config.Project) (string, string, bool, error) {
	barePath := layout.BarePath(mainPath)
	metadataChanged := false
	var err error
	if _, err = os.Stat(barePath); errors.Is(err, os.ErrNotExist) {
		if _, mainErr := os.Stat(mainPath); mainErr == nil || !errors.Is(mainErr, os.ErrNotExist) {
			return "", "", false, git.ErrNeedsMigration
		}
		result, cloneErr := git.CloneIntoLayoutContext(ctx, root, name, project, git.CloneOptions{Logf: materializer.logf})
		if cloneErr != nil {
			return "", "", false, cloneErr
		}
		barePath = result.BarePath
		mainPath = result.MainWorktree
		metadataChanged = true
	} else if err != nil {
		return "", "", false, err
	} else if !git.IsBare(barePath) {
		return "", "", false, git.ErrPathBlocked
	}
	configured, err := git.ConfiguredRemoteURL(barePath, "origin")
	if err != nil || configured != project.Remote {
		return "", "", false, errors.New("configured origin does not match workspace registry")
	}
	if !git.HasFetchRefspec(barePath) {
		if err = git.SetFetchRefspec(barePath); err != nil {
			return "", "", false, err
		}
	}
	if err = git.FetchURLContext(ctx, barePath, project.Remote); err != nil {
		return "", "", false, err
	}
	if err = ensureMainWorktree(ctx, mainPath, barePath, project.DefaultBranch); err != nil {
		return "", "", false, err
	}
	return mainPath, barePath, metadataChanged, nil
}

func ensureMainWorktree(ctx context.Context, mainPath, barePath, branch string) error {
	if git.IsRepo(mainPath) {
		return nil
	}
	if _, err := os.Stat(mainPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return git.ErrPathBlocked
	}
	if branch == "" {
		return git.ErrNeedsBootstrap
	}
	base := ""
	if !git.HasBranch(barePath, branch) {
		if !git.HasRemoteBranch(barePath, "origin", branch) {
			return git.ErrNeedsBootstrap
		}
		base = "origin/" + branch
	}
	if err := git.WorktreeAddContext(ctx, barePath, mainPath, branch, base); err != nil {
		return err
	}
	_ = git.SetBranchUpstream(mainPath, branch, "origin")
	return nil
}

func (materializer *materializer) materializeBranches(ctx context.Context, mainPath, barePath string, project *config.Project) (bool, error) {
	worktrees, err := git.WorktreeList(barePath)
	if err != nil {
		return false, err
	}
	byBranch := make(map[string]string, len(worktrees))
	for _, worktree := range worktrees {
		if !worktree.Bare && !worktree.Detached && worktree.Branch != "" {
			byBranch[worktree.Branch] = worktree.Path
		}
	}
	changed := false
	for index := range project.Branches {
		branch := &project.Branches[index]
		branchChanged, err := materializer.materializeBranch(ctx, mainPath, barePath, project.DefaultBranch, branch, byBranch)
		if err != nil {
			return changed, err
		}
		changed = changed || branchChanged
	}
	return changed, nil
}

func (materializer *materializer) materializeBranch(ctx context.Context, mainPath, barePath, defaultBranch string, branch *config.BranchMeta, byBranch map[string]string) (bool, error) {
	if !materializer.publishedBranchNeedsWorktree(barePath, defaultBranch, branch.Name) {
		return false, nil
	}
	if byBranch[branch.Name] == "" {
		path, err := materializer.addBranchWorktree(ctx, mainPath, barePath, branch.Name)
		if err != nil || path == "" {
			return false, err
		}
		byBranch[branch.Name] = path
	}
	if slices.Contains(branch.Machines, materializer.machine) {
		return false, nil
	}
	branch.Machines = append(branch.Machines, materializer.machine)
	slices.Sort(branch.Machines)
	return true, nil
}

func (materializer *materializer) publishedBranchNeedsWorktree(barePath, defaultBranch, branch string) bool {
	return branch != "" && branch != defaultBranch && git.HasRemoteBranch(barePath, "origin", branch)
}

func (materializer *materializer) addBranchWorktree(ctx context.Context, mainPath, barePath, branch string) (string, error) {
	path := layout.WorktreePathForBranch(mainPath, materializer.machine, branch)
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	base := ""
	if !git.HasBranch(barePath, branch) {
		base = "origin/" + branch
	}
	if err := git.WorktreeAddContext(ctx, barePath, path, branch, base); err != nil {
		return "", err
	}
	_ = git.SetBranchUpstream(path, branch, "origin")
	return path, nil
}

func (materializer *materializer) fastForwardWorktrees(ctx context.Context, barePath string) error {
	worktrees, err := git.WorktreeList(barePath)
	if err != nil {
		return err
	}
	for _, worktree := range worktrees {
		if err = fastForwardWorktree(ctx, worktree); err != nil {
			return err
		}
	}
	return nil
}

func fastForwardWorktree(ctx context.Context, worktree git.Worktree) error {
	if worktree.Bare || worktree.Detached || worktree.Branch == "" || git.HasIndexLock(worktree.Path) || git.IsDirty(worktree.Path) {
		return nil
	}
	ahead, behind, exists := git.AheadBehindRemote(worktree.Path, worktree.Branch, "origin")
	if !exists || ahead != 0 || behind == 0 {
		return nil
	}
	branch, err := git.CurrentBranch(worktree.Path)
	if err != nil || branch != worktree.Branch || git.HasIndexLock(worktree.Path) || git.IsDirty(worktree.Path) {
		return nil
	}
	return git.FastForwardRemoteBranchContext(ctx, worktree.Path, "origin", worktree.Branch)
}
