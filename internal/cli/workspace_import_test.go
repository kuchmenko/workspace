package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
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
	command.SetArgs([]string{"import", tomlPath, "--name", "personal", "--root", root})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "workspace=personal") {
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
	path, err := registry.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := registry.Open(path)
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
	rootCommand.SetArgs([]string{"--root", root, "add", "git@github.com:owner/app.git", "git@github.com:owner/api.git", "--no-clone", "--no-tui"})
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
	store, err = registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.LoadByName(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision == before.Revision || after.State.Projects["app"].Remote != "git@github.com:owner/app.git" || after.State.Projects["api"].Remote != "git@github.com:owner/api.git" {
		t.Fatalf("database-backed add did not advance the revision: %#v", after)
	}
}

func TestFindRegistryWorkspaceRejectsSiblingRoot(t *testing.T) {
	directory := t.TempDir()
	store, err := registry.Open(filepath.Join(directory, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
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
	if _, err = store.Create(context.Background(), "one", firstRoot, workspace); err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(context.Background(), "two", secondRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	found, err := store.Find(context.Background(), secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != second.Name {
		t.Fatalf("found workspace %q for sibling root %q", found.Name, secondRoot)
	}
}
