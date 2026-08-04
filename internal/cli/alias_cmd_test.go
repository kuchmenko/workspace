package cli

import (
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestAliasAddRejectsInvalidName(t *testing.T) {
	setAliasTestWorkspace(t, map[string]string{})
	cmd := newAliasAddCmd()
	cmd.SetArgs([]string{"bad;name", "project"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "may contain only") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestAliasAddRequiresForceToReplaceTargetAlias(t *testing.T) {
	setAliasTestWorkspace(t, map[string]string{"old": "project"})
	cmd := newAliasAddCmd()
	cmd.SetArgs([]string{"new", "project"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already has alias") {
		t.Fatalf("Execute() error = %v", err)
	}
	if ws.Aliases["old"] != "project" || len(ws.Aliases) != 1 {
		t.Fatalf("aliases changed after rejected add: %#v", ws.Aliases)
	}
}

func TestAliasAddForceReplacesTargetAlias(t *testing.T) {
	setAliasTestWorkspace(t, map[string]string{"old": "project", "keep": "other"})
	cmd := newAliasAddCmd()
	cmd.SetArgs([]string{"--force", "new", "project"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, exists := ws.Aliases["old"]; exists {
		t.Fatalf("old alias remains: %#v", ws.Aliases)
	}
	if ws.Aliases["new"] != "project" || ws.Aliases["keep"] != "other" || len(ws.Aliases) != 2 {
		t.Fatalf("aliases = %#v", ws.Aliases)
	}
}

func setAliasTestWorkspace(t *testing.T, aliases map[string]string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	wsRoot = root
	ws = &config.Workspace{
		Meta:     config.Meta{Version: 1, Root: root},
		Projects: map[string]config.Project{"project": {}, "other": {}},
		Groups:   map[string]config.Group{},
		Aliases:  aliases,
	}
	if err := config.Save(root, ws); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
}
