package agent

import "github.com/kuchmenko/workspace/internal/tui"

func (s *sheet) updateLifecycleKey(m *Model, key string) (bool, tui.Model, tui.Cmd) {
	switch key {
	case "a":
		if worktrees := s.visualWorktrees(); len(worktrees) > 0 {
			s.clearVisual()
			m.sheet = nil
			m.openWorktreeArchiveMany(s.target, worktrees)
			m.lifecycle.parentSheet = s
			return true, m, nil
		}
		if row := s.focused(); row != nil && row.kind == rowWorktree && !row.wt.IsMain {
			m.sheet = nil
			m.openWorktreeArchive(s.target, row.wt)
			m.lifecycle.parentSheet = s
			return true, m, nil
		}
		if s.mode == sheetProject {
			m.sheet = nil
			m.openLifecycle(lifecycleScope{kind: lifecycleProject, project: s.target})
			m.lifecycle.action = lifecycleArchiveProjects
			m.lifecycle.parentSheet = s
			m.prepareLifecycle()
			return true, m, nil
		}
		m.sheet = nil
		m.openLifecycle(lifecycleScope{kind: lifecycleGroup, group: s.group, workspaceRoot: s.workspaceRoot})
		m.lifecycle.action = lifecycleArchiveProjects
		m.lifecycle.parentSheet = s
		m.prepareLifecycle()
		return true, m, nil
	case "A":
		m.sheet = nil
		if s.mode == sheetProject {
			m.openLifecycle(lifecycleScope{kind: lifecycleProject, project: s.target})
		} else {
			m.openLifecycle(lifecycleScope{kind: lifecycleGroup, group: s.group, workspaceRoot: s.workspaceRoot})
		}
		m.lifecycle.parentSheet = s
		return true, m, nil
	case "d":
		if worktrees := s.visualWorktrees(); len(worktrees) > 0 {
			s.clearVisual()
			m.sheet = nil
			m.openWorktreeDeleteMany(s.target, worktrees)
			m.lifecycle.parentSheet = s
			return true, m, nil
		}
		if row := s.focused(); row != nil && row.kind == rowWorktree && !row.wt.IsMain {
			s.pendingDel = row.wt
			m.sheet = nil
			m.openWorktreeDelete(s.target, row.wt)
			m.lifecycle.parentSheet = s
			return true, m, nil
		}
	}
	return false, m, nil
}
