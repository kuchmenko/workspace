package agent

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingDelete {
		m.pendingDelete = false
		if msg.String() == "y" && m.deleteItem != nil {
			it := m.deleteItem
			m.deleteItem = nil

			projID := ""
			if it.parentProj != nil {
				projID = it.parentProj.ID
			}
			wsRoot := m.workspaceRootFor(it.parentProj)
			if err := DeleteWorktreeWithRegistry(it.parentProj.Path, it.worktree.Path, false, wsRoot, projID, it.worktree.Branch); err != nil {
				m.statusMsg = err.Error()
				return m, nil
			}
			m.wtCache.Invalidate(it.parentProj.Path)
			m.rebuildItems()
			m.ensureVisible()
			m.statusMsg = "worktree deleted"
			return m, nil
		}
		m.deleteItem = nil
		m.statusMsg = ""
		return m, nil
	}

	m.statusMsg = ""
	item := m.currentItem()

	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < len(m.headerChips) {
			c := m.headerChips[idx]
			m.chipTarget = &c
			m.mode = viewChipAction
			return m, nil
		}
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		if m.cursor+1 < len(m.items) {
			m.cursor++
			m.ensureVisible()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}

	case "enter":
		if item == nil {
			break
		}
		switch item.kind {
		case KindGroup:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
		case KindProject:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
		case KindWorktree:
			m.Launch = &LaunchRequest{Cwd: item.path}
			return m, tea.Quit
		case KindPortal:
			if item.session != nil {
				m.Launch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
				return m, tea.Quit
			}
		}

	case "p":

		if item != nil && item.path != "" && (item.kind == KindGroup || item.kind == KindProject || item.kind == KindWorktree) {
			m.pendingLaunch = &LaunchRequest{Cwd: item.path}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		}

		if item != nil && item.kind == KindPortal && item.session != nil {
			m.pendingLaunch = &LaunchRequest{Cwd: item.session.Cwd, ResumeID: item.session.ID}
			m.promptInput = ""
			m.mode = viewPromptInput
			return m, nil
		}

	case "w":

		if item != nil && item.kind == KindProject {
			m.wtNoLaunch = true
			m.wtBranch = ""
			m.wtField = 0
			m.popupProj = item.project
			m.mode = viewNewWorktree
			return m, nil
		}

	case "e":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.popupProj = item.project
			m.editGroup = item.project.Group
			m.editCategory = config.Category(item.project.Category)
			if m.editCategory == "" {
				m.editCategory = config.CategoryPersonal
			}
			m.editField = 0
			m.editErr = ""
			m.mode = viewEditProject
			return m, nil
		}

	case "l", "right":
		if item != nil && item.path != "" {
			m.Launch = &LaunchRequest{Cwd: item.path, ShellOnly: true}
			return m, tea.Quit
		}

	case "f":

		if item != nil && item.kind == KindProject && item.project != nil {
			m.toggleFavoriteFor(item.project)
		}
		if item != nil && item.kind == KindGroup && item.group != "" {
			m.toggleFavoriteGroup(item.group)
		}

	case "h", "left":
		if item != nil {
			switch {
			case item.kind == KindProject && m.expanded["proj:"+item.project.ID]:
				m.expanded["proj:"+item.project.ID] = false
				m.rebuildItems()
				m.ensureVisible()
			case item.kind == KindProject && item.project.Group != "":
				m.expanded[item.project.Group] = false
				m.rebuildItems()
				m.jumpToGroup(item.project.Group)
			case (item.kind == KindWorktree || item.kind == KindPortal) && item.parentProj != nil:
				m.expanded["proj:"+item.parentProj.ID] = false
				m.rebuildItems()
				m.jumpToProject(item.parentProj.ID)
			case item.kind == KindGroup && m.expanded[item.group]:
				m.expanded[item.group] = false
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "tab":

		if item != nil {
			switch item.kind {
			case KindGroup:
				m.toggleExpand(item.group)
			case KindProject:
				key := "proj:" + item.project.ID
				m.expanded[key] = !m.expanded[key]
				m.rebuildItems()
				m.ensureVisible()
			}
		}

	case "d":
		if item != nil && item.kind == KindWorktree && item.worktree != nil && !item.worktree.IsMain && item.parentProj != nil {
			wt := item.worktree
			if git.IsDirty(wt.Path) {
				m.statusMsg = "cannot delete: uncommitted changes"
				break
			}
			ahead, _, hasUpstream := git.AheadBehind(wt.Path, wt.Branch)
			if hasUpstream && ahead > 0 {
				m.statusMsg = fmt.Sprintf("cannot delete: %d unpushed commit(s)", ahead)
				break
			}

			name := worktreeDisplayName(*wt)
			m.statusMsg = fmt.Sprintf("delete %s? y to confirm", name)
			m.pendingDelete = true
			m.deleteItem = item
		}

	case "s", "/":
		m.flashGlobal = false
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "S":

		m.flashGlobal = true
		m.savedExpanded = make(map[string]bool)
		for k, v := range m.expanded {
			m.savedExpanded[k] = v
		}

		for _, ws := range m.workspaces {
			for _, g := range ws.Groups {
				m.expanded[g] = true
			}
			for i := range ws.Projects {
				m.expanded["proj:"+ws.Projects[i].ID] = true
			}
		}
		m.rebuildItems()
		m.mode = viewFlash
		m.flashQuery = ""
		m.recomputeFlash()

	case "?", " ":
		m.whichKeyLevel = 0
		m.mode = viewWhichKey

	case "G":
		m.cursor = len(m.items) - 1
		m.ensureVisible()
	case "g":
		m.cursor = 0
		m.scroll = 0
	}
	return m, nil
}
