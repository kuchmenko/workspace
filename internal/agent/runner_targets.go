package agent

import (
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/runner"
)

func (m *Model) runnerWorkspaceName(root string) string {
	for _, workspace := range m.workspaces {
		if workspace.Root == root {
			return workspace.Name
		}
	}
	return ""
}

func (m *Model) groupRunnerTarget(root, group string) config.RunnerConfig {
	return config.RunnerConfig{Workspace: m.runnerWorkspaceName(root), Group: group}
}

func (m *Model) projectRunnerTarget(project *Project) config.RunnerConfig {
	if project == nil {
		return config.RunnerConfig{}
	}
	return config.RunnerConfig{Workspace: m.runnerWorkspaceName(project.WorkspaceRoot), Project: project.ID}
}

func (m *Model) worktreeRunnerTarget(project *Project, worktree *Worktree) config.RunnerConfig {
	target := m.projectRunnerTarget(project)
	if worktree != nil && !worktree.IsMain {
		target.Worktree = worktree.Branch
	}
	return target
}

func (m *Model) runnerTargetForPath(path string) (config.RunnerConfig, bool) {
	canonical := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		canonical = resolved
	}
	for wi := range m.workspaces {
		workspace := &m.workspaces[wi]
		for _, group := range workspace.Groups {
			if sameRunnerPath(groupRootPath(workspace.Root, group), canonical) {
				return m.groupRunnerTarget(workspace.Root, group), true
			}
		}
		for pi := range workspace.Projects {
			project := &workspace.Projects[pi]
			if sameRunnerPath(project.Path, canonical) {
				return m.projectRunnerTarget(project), true
			}
			for ti := range project.WorktreeInventory {
				worktree := &project.WorktreeInventory[ti]
				if !worktree.IsMain && sameRunnerPath(worktree.Path, canonical) {
					return m.worktreeRunnerTarget(project, worktree), true
				}
			}
		}
	}
	return config.RunnerConfig{}, false
}

func sameRunnerPath(path, canonical string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path == canonical
}

func (m *Model) targetRunnerInfo(target config.RunnerConfig, path string) (runner.Info, bool) {
	key := runner.TargetKey(target)
	for _, info := range m.runnerInfos {
		if info.Definition.ID != "" && runner.TargetKey(info.Definition) == key {
			return info, true
		}
	}
	canonical := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		canonical = resolved
	}
	for _, info := range m.runnerInfos {
		if info.Definition.ID == "" && info.Path == canonical {
			return info, true
		}
	}
	return runner.Info{}, false
}

func (m *Model) runnerBadge(target config.RunnerConfig, path string) string {
	info, found := m.targetRunnerInfo(target, path)
	if !found {
		return ""
	}
	switch info.Status {
	case runner.StatusRunning:
		return "runner on"
	case runner.StatusOccupied:
		return "runner external"
	case runner.StatusMissing:
		return "runner missing"
	default:
		return "runner off"
	}
}
