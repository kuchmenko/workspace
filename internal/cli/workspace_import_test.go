package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncnode"
)

func TestWorkspaceImportAndExport(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	root := filepath.Join(directory, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(directory, "workspace.toml")
	body := "[meta]\nversion = 1\n\n[groups]\n\n[projects]\n\n[aliases]\nws = \"workspace\"\n"
	if err := os.WriteFile(tomlPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := newWorkspaceCmd()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"import", tomlPath, "--name", "personal", "--root", root, "--recovery-key", filepath.Join(directory, "offline", "recovery.key")})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "workspace=personal") || !strings.Contains(output.String(), "recovery key created") {
		t.Fatalf("import output = %q", output.String())
	}
	output.Reset()
	command = newWorkspaceCmd()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"export", "personal"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ws = \"workspace\"") || strings.Contains(output.String(), root) {
		t.Fatalf("export output = %q", output.String())
	}
	paths, err := syncnode.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	store, err := syncnode.OpenStore(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadByName(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	rootCommand := NewRootCmd()
	rootCommand.SetOut(&output)
	rootCommand.SetErr(&output)
	rootCommand.SetArgs([]string{"--root", root, "add", "git@github.com:owner/app.git", "--name", "app", "--no-clone", "--no-tui"})
	if err = rootCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	afterBody, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBody) != body {
		t.Fatal("source TOML changed after database-backed add")
	}
	store, err = syncnode.OpenStore(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.LoadByName(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if after.Head == before.Head || after.State.Projects["app"].Remote != "git@github.com:owner/app.git" {
		t.Fatalf("database-backed add did not create a child revision: %#v", after)
	}
	rootCommand = NewRootCmd()
	rootCommand.SetArgs([]string{"--root", root, "sync"})
	if err = rootCommand.Execute(); err == nil || !strings.Contains(err.Error(), "legacy Git-backed sync is disabled") {
		t.Fatalf("legacy sync error = %v", err)
	}
	explorer := newExplorerCmd()
	if err = explorer.Execute(); err == nil || !strings.Contains(err.Error(), "explorer is disabled") {
		t.Fatalf("Explorer error = %v", err)
	}
}

func TestFindNodeWorkspaceRejectsSiblingRoot(t *testing.T) {
	directory := t.TempDir()
	store, err := syncnode.OpenStore(filepath.Join(directory, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	identity, err := syncnode.OpenOrCreateIdentity(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := syncnode.CreateRecoveryKey(filepath.Join(directory, "recovery.key"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := config.DecodeWorkspaceForImport([]byte("[meta]\nversion = 1\n[groups]\n[projects]\n[aliases]\n"))
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := filepath.Join(directory, "one")
	secondRoot := filepath.Join(directory, "two")
	for _, root := range []string{firstRoot, secondRoot} {
		if err = os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Import(context.Background(), "one", firstRoot, workspace, identity, recovery); err != nil {
		t.Fatal(err)
	}
	second, err := store.Import(context.Background(), "two", secondRoot, workspace, identity, recovery)
	if err != nil {
		t.Fatal(err)
	}
	found, err := findNodeWorkspace(context.Background(), store, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != second.ID {
		t.Fatalf("found workspace %q for sibling root %q", found.Name, secondRoot)
	}
}
