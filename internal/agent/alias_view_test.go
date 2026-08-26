package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestExplorerAliasOpensForSelectedProjectAndUpdatesBadge(t *testing.T) {
	project := Project{ID: "dotfiles", Name: "dotfiles", WorkspaceRoot: "/ws", Path: "/ws/dotfiles"}
	m := NewModel([]WorkspaceData{{Root: "/ws", Projects: []Project{project}, Aliases: map[string]string{"dot": "dotfiles"}}})
	m.items = []listItem{{kind: KindProject, project: &m.workspaces[0].Projects[0], workspaceRoot: "/ws"}}
	m.updateList(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'a'}})
	if m.mode != viewAlias || m.aliasInput.Value() != "dot" || m.aliasTarget.target != "dotfiles" {
		t.Fatalf("alias editor = mode %v value %q target %#v", m.mode, m.aliasInput.Value(), m.aliasTarget)
	}
	m.updateWorkspaceAlias("/ws", "dotfiles", "dtfls")
	line := m.renderProject(m.items[0], false, false, false, 0, 100, false)
	if !strings.Contains(line, "alias dtfls") {
		t.Fatalf("project row does not track alias: %q", line)
	}
}

func TestExplorerAliasChecksAllWorkspacesAndWritesCombinedShellState(t *testing.T) {
	isolateRegistryFixture(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	saveRegistryFixture(t, first, aliasWorkspace("one", "onealias"))
	saveRegistryFixture(t, second, aliasWorkspace("two", ""))

	if warning, err := saveExplorerAlias(second, "two", "twoalias"); err != nil || warning != "" {
		t.Fatalf("save alias = warning %q error %v", warning, err)
	}
	statePath := filepath.Join(os.Getenv("XDG_STATE_HOME"), "ws", "aliases.zsh")
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, aliasName := range []string{"onealias", "twoalias"} {
		if !strings.Contains(string(state), "alias "+aliasName+"=") {
			t.Fatalf("combined shell state does not contain %q: %q", aliasName, state)
		}
	}
	if _, err := saveExplorerAlias(second, "two", "onealias"); err == nil || !strings.Contains(err.Error(), "workspace \"first\"") {
		t.Fatalf("global collision error = %v", err)
	}
	if _, err := saveExplorerAlias(second, "two", ""); err != nil {
		t.Fatal(err)
	}
	if alias := loadRegistryFixture(t, second).Aliases["twoalias"]; alias != "" {
		t.Fatalf("removed alias still targets %q", alias)
	}
}

func aliasWorkspace(project, aliasName string) *config.Workspace {
	aliases := map[string]string{}
	if aliasName != "" {
		aliases[aliasName] = project
	}
	return &config.Workspace{
		Meta:     config.Meta{Version: 1},
		Groups:   map[string]config.Group{},
		Projects: map[string]config.Project{project: {Path: project, Status: config.StatusActive}},
		Aliases:  aliases,
	}
}
