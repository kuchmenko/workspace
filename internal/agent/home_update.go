package agent

import (
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) footerHints() (actions, nav string) {
	nav = "j/k:move  g/G:first/last  ^d/^u:half  ^f/^b:page  ?:more"
	item := m.currentItem()
	if item == nil {
		return "⏎:open  s:find  S:all", nav
	}
	switch item.kind {
	case KindWorkspace:
		actions = "⏎/l:expand  h:back  a:alias  tab:expand"
	case KindGroup:
		if item.projectionGroup {
			actions = "⏎/l:open  h:back  tab:expand"
		} else {
			actions = "⏎/l:open  h:back  a:alias  r:runner  f:favorite  tab:expand  A:Activity  M:maintenance  S:search"
		}
	case KindProject:
		actions = "⏎/l:open  h:back  a:alias  r:runner  w:new  e:edit  f:favorite  A:Activity  M:maintenance  S:search"
	default:
		actions = "⏎:open"
	}
	return actions, nav
}

func (m *Model) updateList(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	m.statusMsg = ""
	key := msg.String()
	if model, cmd, handled := m.updateHomeSessionKey(key); handled {
		return model, cmd
	}
	if model, cmd, handled := m.updateHomeActionKey(key, m.currentItem()); handled {
		return model, cmd
	}
	if handled := m.updateHomeNavigationKey(key, m.currentItem()); handled {
		return m, nil
	}
	return m, nil
}

func (m *Model) updateHomeSessionKey(key string) (tui.Model, tui.Cmd, bool) {
	switch key {
	case "q":
		if m.jobsRunning() {
			m.statusMsg = "actions are still queued or running · A Open Activity"
			return m, nil, true
		}
		return m, tui.Quit, true
	case "v":
		m.switchHomeProjection()
	case "o":
		m.reverseRecentOrder()
	case "A":
		m.openActivity(nil)
	case "R":
		m.openRunnerView()
	case "M":
		m.openLifecycle(lifecycleScope{kind: lifecycleGlobal})
	case "S":
		m.openGlobalSearch()
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m *Model) updateHomeActionKey(key string, item *listItem) (tui.Model, tui.Cmd, bool) {
	switch key {
	case "a":
		m.openItemAlias(item)
	case "r":
		model, cmd := m.openItemRunner(item)
		return model, cmd, true
	case "enter", "l", "right":
		model, cmd := m.openCurrentItem()
		return model, cmd, true
	case "w":
		m.openWorktreeForm(item)
	case "e":
		m.openProjectEditForm(item)
	case "f":
		model, cmd := m.toggleItemFavorite(item)
		return model, cmd, true
	case "s", "/":
		m.openLocalSearch()
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m *Model) updateHomeNavigationKey(key string, item *listItem) bool {
	switch key {
	case "j", "down":
		m.moveHomeCursor(1)
	case "k", "up":
		m.moveHomeCursor(-1)
	case "ctrl+d":
		m.moveHomeCursor(max(1, m.listHeight()/2))
	case "ctrl+u":
		m.moveHomeCursor(-max(1, m.listHeight()/2))
	case "ctrl+f", "pgdn":
		m.moveHomeCursor(m.listHeight())
	case "ctrl+b", "pgup":
		m.moveHomeCursor(-m.listHeight())
	case "h", "left":
		m.collapseItem(item)
	case "tab":
		m.toggleItem(item)
	case "G", "end":
		m.moveHomeTo(len(m.items) - 1)
	case "g", "home":
		m.moveHomeTo(0)
	default:
		return false
	}
	return true
}

func (m *Model) openItemRunner(item *listItem) (tui.Model, tui.Cmd) {
	if item != nil && item.kind == KindProject {
		return m.openRunnerForTarget(m.projectRunnerTarget(item.project), item.path)
	}
	if item != nil && item.kind == KindGroup && !item.projectionGroup {
		return m.openRunnerForTarget(m.groupRunnerTarget(item.workspaceRoot, item.group), item.path)
	}
	return m, nil
}

func (m *Model) openWorktreeForm(item *listItem) {
	if item == nil || item.kind != KindProject {
		return
	}
	m.wtBranch.SetValue("")
	m.wtBranch.Focus()
	m.wtField = 0
	m.popupProj = item.project
	m.mode = viewNewWorktree
}

func (m *Model) openProjectEditForm(item *listItem) {
	if item == nil || item.kind != KindProject || item.project == nil {
		return
	}
	m.popupProj = item.project
	m.editGroup.SetValue(item.project.Group)
	m.editGroup.Focus()
	m.editCategory = config.Category(item.project.Category)
	if m.editCategory == "" {
		m.editCategory = config.CategoryPersonal
	}
	m.editField = 0
	m.editErr = ""
	m.mode = viewEditProject
}

func (m *Model) toggleItemFavorite(item *listItem) (tui.Model, tui.Cmd) {
	if item != nil && item.kind == KindProject && item.project != nil {
		return m, m.toggleFavoriteFor(item.project)
	}
	if item != nil && item.kind == KindGroup && item.group != "" && !item.projectionGroup {
		return m, m.toggleFavoriteGroup(item.workspaceRoot, item.group)
	}
	return m, nil
}

func (m *Model) collapseItem(item *listItem) {
	if item == nil {
		return
	}
	switch {
	case item.kind == KindProject && item.expandKey != "":
		m.expanded[item.expandKey] = false
		m.rebuildItems()
		m.jumpToExpandKey(item.expandKey)
	case item.kind == KindGroup && m.expanded[item.expandKey]:
		m.expanded[item.expandKey] = false
		m.rebuildItems()
		m.ensureVisible()
	case item.kind == KindGroup && item.parentExpandKey != "":
		m.jumpToExpandKey(item.parentExpandKey)
	case item.kind == KindWorkspace && m.expanded[item.expandKey]:
		m.expanded[item.expandKey] = false
		m.rebuildItems()
		m.ensureVisible()
	}
}

func (m *Model) toggleItem(item *listItem) {
	if item != nil && (item.kind == KindWorkspace || item.kind == KindGroup) {
		m.toggleExpand(item.expandKey)
	}
}

func (m *Model) saveExplorerPreferences() {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return
	}
	machine.ExplorerView, machine.RecentOrder = m.homeView, m.recentOrder
	_ = config.SaveMachineConfig(machine)
}
