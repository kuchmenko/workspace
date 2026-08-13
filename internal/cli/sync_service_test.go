package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestServiceCommandWorkspaceClassification(t *testing.T) {
	service := newSyncServiceCmd()
	for _, command := range service.Commands() {
		want := command.Name() != "import" && command.Name() != "resolve"
		if got := commandSkipsWorkspace(command); got != want {
			t.Fatalf("%s skips workspace=%v, want %v", command.Name(), got, want)
		}
	}
}

func TestServiceResolveCommandExists(t *testing.T) {
	command, _, err := newSyncServiceCmd().Find([]string{"resolve"})
	if err != nil || command.Name() != "resolve" {
		t.Fatalf("resolve command: %v %v", command, err)
	}
}

func TestRefuseTrackedWorkspaceResolvesDotfilesSymlink(t *testing.T) {
	repository := t.TempDir()
	testutil.RunGit(t, repository, "init")
	registry := filepath.Join(repository, "workspace.toml")
	if err := os.WriteFile(registry, []byte("[meta]\nversion = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, repository, "add", "workspace.toml")
	root := t.TempDir()
	if err := os.Symlink(registry, filepath.Join(root, "workspace.toml")); err != nil {
		t.Fatal(err)
	}
	if err := refuseTrackedWorkspace(root); err == nil || !strings.Contains(err.Error(), "remove it from Git tracking") {
		t.Fatalf("tracked registry error = %v", err)
	}
	testutil.RunGit(t, repository, "rm", "--cached", "workspace.toml")
	if err := refuseTrackedWorkspace(root); err != nil {
		t.Fatalf("untracked registry rejected: %v", err)
	}
}
