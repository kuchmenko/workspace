package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCommandsCreateAndList(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	workspace := t.TempDir()
	cmd := newWorkspaceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"create", workspace, "--name", "personal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace create: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "workspace=personal") || !strings.Contains(got, "root="+workspace) {
		t.Fatalf("workspace create output = %q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "workspace.toml")); !os.IsNotExist(err) {
		t.Fatalf("workspace create wrote workspace.toml: %v", err)
	}

	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "personal\t"+workspace+"\n") {
		t.Fatalf("workspace list output = %q", got)
	}
}

func TestWorkspaceCreateDefaultsToCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	cmd := newWorkspaceCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"create", "--name", "personal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace create: %v", err)
	}
	out := bytes.Buffer{}
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	if !strings.Contains(out.String(), "personal\t"+cwd+"\n") {
		t.Fatalf("workspace list output = %q", out.String())
	}
}

func TestRootWorkspaceListDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if _, err := os.Stat(filepath.Join(cwd, "workspace.toml")); !os.IsNotExist(err) {
		t.Fatalf("test cwd unexpectedly contains workspace.toml: %v", err)
	}

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"workspace", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("ws workspace list outside a workspace: %v", err)
	}
}

func TestExplorerDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	explorer, _, err := root.Find([]string{"explorer"})
	if err != nil {
		t.Fatalf("find explorer command: %v", err)
	}
	if err := root.PersistentPreRunE(explorer, nil); err != nil {
		t.Fatalf("explorer pre-run outside a workspace: %v", err)
	}
}

func TestAgentAliasIsRemoved(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"agent"}); err == nil {
		t.Fatal("ws agent should not resolve to a command")
	}
}

func TestExplorerShellLaunchAliasIsRemoved(t *testing.T) {
	root := NewRootCmd()
	cmd, args, err := root.Find([]string{"explorer", "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ValidateArgs(args); err == nil {
		t.Fatal("ws explorer launch should fail")
	}
}
