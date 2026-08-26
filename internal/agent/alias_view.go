package agent

import (
	"fmt"
	"sort"
	"strings"

	workspacealias "github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/kuchmenko/workspace/internal/tui"
)

const workspaceAliasRootTarget = workspacealias.RootTarget

type explorerAliasTarget struct {
	workspaceRoot string
	target        string
	label         string
	existing      string
}

func (m *Model) openItemAlias(item *listItem) {
	target, ok := m.aliasTargetForItem(item)
	if !ok {
		return
	}
	m.openAlias(target)
}

func (m *Model) openAlias(target explorerAliasTarget) {
	m.aliasTarget = target
	m.aliasInput.SetValue(target.existing)
	m.aliasInput.Focus()
	m.aliasError = ""
	m.mode = viewAlias
}

func (m *Model) aliasTargetForItem(item *listItem) (explorerAliasTarget, bool) {
	if item == nil || item.projectionGroup || item.kind == KindWorktree {
		return explorerAliasTarget{}, false
	}
	target := explorerAliasTarget{workspaceRoot: item.workspaceRoot}
	switch item.kind {
	case KindWorkspace:
		target.target, target.label = workspacealias.RootTarget, item.workspaceName
	case KindGroup:
		target.target, target.label = item.group, "@"+item.group
	case KindProject:
		if item.project == nil {
			return explorerAliasTarget{}, false
		}
		target.workspaceRoot = item.project.WorkspaceRoot
		target.target, target.label = item.project.ID, item.project.Name
	default:
		return explorerAliasTarget{}, false
	}
	target.existing = m.aliasForTarget(target.workspaceRoot, target.target)
	return target, target.workspaceRoot != "" && target.target != ""
}

func (m *Model) aliasForTarget(root, target string) string {
	for _, workspace := range m.workspaces {
		if workspace.Root != root {
			continue
		}
		var names []string
		for name, candidate := range workspace.Aliases {
			if candidate == target {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func (m *Model) updateAlias(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeAlias()
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.aliasInput.Value())
		warning, err := saveExplorerAlias(m.aliasTarget.workspaceRoot, m.aliasTarget.target, name)
		if err != nil {
			m.aliasError = err.Error()
			return m, nil
		}
		m.updateWorkspaceAlias(m.aliasTarget.workspaceRoot, m.aliasTarget.target, name)
		label := m.aliasTarget.label
		m.closeAlias()
		if warning != "" {
			m.statusMsg = warning
		} else if name == "" {
			m.statusMsg = "removed alias for " + label
		} else {
			m.statusMsg = "saved alias " + name + " for " + label
		}
		return m, nil
	default:
		var cmd tui.Cmd
		m.aliasInput, cmd = m.aliasInput.Update(msg)
		m.aliasError = ""
		return m, cmd
	}
}

func (m *Model) closeAlias() {
	m.aliasInput.Blur()
	m.aliasError = ""
	m.aliasTarget = explorerAliasTarget{}
	m.mode = viewList
}

func (m *Model) updateWorkspaceAlias(root, target, name string) {
	for i := range m.workspaces {
		workspace := &m.workspaces[i]
		if workspace.Root != root {
			continue
		}
		if workspace.Aliases == nil {
			workspace.Aliases = map[string]string{}
		}
		for existing, candidate := range workspace.Aliases {
			if candidate == target {
				delete(workspace.Aliases, existing)
			}
		}
		if name != "" {
			workspace.Aliases[name] = target
		}
		return
	}
}

func saveExplorerAlias(root, target, name string) (string, error) {
	if root == "" || target == "" {
		return "", fmt.Errorf("alias target is unavailable")
	}
	registered, err := localWorkspaces()
	if err != nil {
		return "", err
	}
	existing, err := validateExplorerAlias(registered, root, target, name)
	if err != nil {
		return "", err
	}
	if name != "" && name != existing {
		if path, conflict := workspacealias.ShellConflict(name); conflict {
			return "", fmt.Errorf("alias %q would shadow existing command at %s", name, path)
		}
	}
	if err := mutateExplorerAlias(root, target, name); err != nil {
		return "", err
	}
	if err := writeAllAliasState(); err != nil {
		return "alias saved, but shell state was not updated: " + err.Error(), nil
	}
	return "", nil
}

func validateExplorerAlias(registered []registry.Workspace, root, target, name string) (string, error) {
	if name != "" {
		if err := workspacealias.ValidateName(name); err != nil {
			return "", err
		}
	}
	existing := ""
	for _, workspace := range registered {
		for aliasName, aliasTarget := range workspace.State.Aliases {
			if workspace.Root == root && aliasTarget == target {
				existing = aliasName
			}
			if name != "" && aliasName == name && (workspace.Root != root || aliasTarget != target) {
				return "", fmt.Errorf("alias %q is already assigned in workspace %q", name, workspace.Name)
			}
		}
	}
	return existing, nil
}

func mutateExplorerAlias(root, target, name string) error {
	return mutateRegistryWorkspace(root, func(workspace *config.Workspace) error {
		if target != workspacealias.RootTarget {
			if _, project := workspace.Projects[target]; !project {
				if _, group := workspace.Groups[target]; !group {
					return fmt.Errorf("alias target %q no longer exists", target)
				}
			}
		}
		for aliasName, aliasTarget := range workspace.Aliases {
			if aliasTarget == target {
				delete(workspace.Aliases, aliasName)
			}
		}
		if name != "" {
			workspace.Aliases[name] = target
		}
		return nil
	})
}

func writeAllAliasState() error {
	registered, err := localWorkspaces()
	if err != nil {
		return err
	}
	var resolved []workspacealias.Resolved
	for _, workspace := range registered {
		resolved = append(resolved, workspacealias.ResolveAll(workspace.State, workspace.Root)...)
	}
	return workspacealias.WriteResolvedStateFile(resolved)
}

func (m *Model) viewAlias() string {
	popupW := min(58, max(32, m.width-6))
	innerW := max(1, popupW-6)
	title := "Edit alias"
	if m.aliasTarget.label != "" {
		title += ": " + presentLabel(m.aliasTarget.label)
	}
	lines := []string{
		popupTitleStyle.Width(innerW).Render(title),
		"",
		popupDimStyle.Width(innerW).Render("  Shell alias"),
		popupSelectedStyle.Width(innerW).Render("  " + m.aliasInput.View()),
	}
	if m.aliasError != "" {
		lines = append(lines, "", statusMsgStyle.Width(innerW).Render("  "+presentLabel(m.aliasError)))
	}
	lines = append(lines, "", popupDimStyle.Width(innerW).Render("  Enter save · empty removes · Esc cancel"))
	panel := popupBorderStyle.Width(popupW - 2).Render(tui.JoinVertical(tui.Left, lines...))
	return tui.Overlay(tui.DimCanvas(m.width, m.height, m.viewList()), panel, m.width, m.height)
}
