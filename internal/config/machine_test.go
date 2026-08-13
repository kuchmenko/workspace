package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestWorkspaceRootsCanonicalizeDeduplicateAndSort(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	alpha := filepath.Join(parent, "alpha")
	zulu := filepath.Join(parent, "zulu")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(zulu, 0o755); err != nil {
		t.Fatal(err)
	}
	alphaLink := filepath.Join(parent, "alpha-link")
	if err := os.Symlink(alpha, alphaLink); err != nil {
		t.Fatal(err)
	}

	if _, err := AddWorkspaceRoot(zulu); err != nil {
		t.Fatalf("AddWorkspaceRoot zulu: %v", err)
	}
	if got, err := AddWorkspaceRoot(alphaLink); err != nil {
		t.Fatalf("AddWorkspaceRoot alpha symlink: %v", err)
	} else if got != alpha {
		t.Fatalf("canonical alpha root = %q, want %q", got, alpha)
	}
	if got, err := AddWorkspaceRoot(alpha); err != nil {
		t.Fatalf("duplicate canonical root should be idempotent: %v", err)
	} else if got != alpha {
		t.Fatalf("duplicate canonical root = %q, want %q", got, alpha)
	}

	got, err := ListWorkspaceRoots()
	if err != nil {
		t.Fatalf("ListWorkspaceRoots: %v", err)
	}
	want := []string{alpha, zulu}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace roots = %v, want %v", got, want)
	}

	if removed, err := RemoveWorkspaceRoot(alphaLink); err != nil {
		t.Fatalf("RemoveWorkspaceRoot: %v", err)
	} else if removed != alpha {
		t.Fatalf("removed root = %q, want %q", removed, alpha)
	}
	got, err = ListWorkspaceRoots()
	if err != nil {
		t.Fatalf("ListWorkspaceRoots after remove: %v", err)
	}
	if !reflect.DeepEqual(got, []string{zulu}) {
		t.Fatalf("workspace roots after remove = %v, want [%s]", got, zulu)
	}
	if _, err := RemoveWorkspaceRoot(alpha); err == nil {
		t.Fatal("removing an unregistered root should fail")
	}
}

func TestLoadMachineConfigNormalizesExistingRoots(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	realRoot := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	path, err := MachineConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "machine_name = \"linux\"\nworkspace_roots = [\"" + link + "\", \"" + realRoot + "\", \"\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadMachineConfig()
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg.WorkspaceRoots, []string{realRoot}) {
		t.Fatalf("workspace roots = %v, want [%s]", cfg.WorkspaceRoots, realRoot)
	}
}

func TestMachineServiceBindingsRoundTripNormalizeAndRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	cfg := &MachineConfig{MachineName: "linux", Service: &MachineService{ID: "service", Endpoint: "https://service", Bindings: []WorkspaceBinding{{Root: link, WorkspaceID: "old"}, {Root: root, WorkspaceID: "workspace"}}}}
	if err := SaveMachineConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Service.Bindings) != 1 || loaded.Service.Bindings[0].Root != root || loaded.Service.Bindings[0].WorkspaceID != "workspace" {
		t.Fatalf("bindings=%+v", loaded.Service.Bindings)
	}
	if binding, ok, err := loaded.Binding(link); err != nil || !ok || binding.WorkspaceID != "workspace" {
		t.Fatalf("binding=%+v ok=%v err=%v", binding, ok, err)
	}
	if removed, err := loaded.RemoveBinding(link); err != nil || !removed || len(loaded.Service.Bindings) != 0 {
		t.Fatalf("removed=%v bindings=%+v err=%v", removed, loaded.Service.Bindings, err)
	}
}

func TestLoadMachineConfigMigratesLegacyDaemonRoots(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	alpha := t.TempDir()
	beta := t.TempDir()
	if err := SaveMachineConfig(&MachineConfig{
		MachineName:    "linux",
		WorkspaceRoots: []string{beta},
	}); err != nil {
		t.Fatalf("SaveMachineConfig: %v", err)
	}
	configPath, err := MachineConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(filepath.Dir(configPath), "daemon.toml")
	body := "[daemon]\nsocket = 42\n\n[[workspace]]\nroot = \"" + alpha + "\"\nauto_bootstrap = \"ignored\"\n\n[[workspace]]\nroot = \"" + beta + "\"\npoll_interval = [1, 2]\n"
	if err := os.WriteFile(legacyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadMachineConfig()
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if cfg.MachineName != "linux" {
		t.Fatalf("machine name = %q, want linux", cfg.MachineName)
	}
	want := []string{alpha, beta}
	if !reflect.DeepEqual(cfg.WorkspaceRoots, want) {
		t.Fatalf("migrated roots = %v, want %v", cfg.WorkspaceRoots, want)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy daemon config still exists: %v", err)
	}

	reloaded, err := LoadMachineConfig()
	if err != nil {
		t.Fatalf("second LoadMachineConfig: %v", err)
	}
	if !reflect.DeepEqual(reloaded.WorkspaceRoots, want) {
		t.Fatalf("roots after second load = %v, want %v", reloaded.WorkspaceRoots, want)
	}
}

func TestLegacyDaemonConfigRemainsWhenMachineConfigSaveFails(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "ws")
	if err := os.MkdirAll(filepath.Join(dir, "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "daemon.toml")
	if err := os.WriteFile(legacyPath, []byte("[[workspace]]\nroot = \"/workspace\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := migrateLegacyDaemonConfig(&MachineConfig{})
	if err == nil {
		t.Fatal("migration should fail when config.toml is a directory")
	}
	if _, statErr := os.Stat(legacyPath); statErr != nil {
		t.Fatalf("legacy daemon config should remain after save failure: %v", statErr)
	}
}

func TestRemoveLegacyDaemonConfigIgnoresConcurrentRemoval(t *testing.T) {
	if err := removeLegacyDaemonConfig(filepath.Join(t.TempDir(), "daemon.toml")); err != nil {
		t.Fatalf("remove missing legacy daemon config: %v", err)
	}
}

func TestLiveLegacyDaemonPIDUsesXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "ws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(want)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid, err := liveLegacyDaemonPID()
	if err != nil {
		t.Fatalf("liveLegacyDaemonPID: %v", err)
	}
	if pid != want {
		t.Fatalf("live legacy daemon PID = %d, want %d", pid, want)
	}
}

func TestLiveLegacyDaemonPIDIgnoresStaleProcess(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "ws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte("1073741824"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid, err := liveLegacyDaemonPID()
	if err != nil {
		t.Fatalf("liveLegacyDaemonPID: %v", err)
	}
	if pid != 0 {
		t.Fatalf("stale legacy daemon PID = %d, want 0", pid)
	}
}

func TestEnsureLegacyDaemonStoppedReturnsActionableError(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "ws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	err := EnsureLegacyDaemonStopped()
	if err == nil {
		t.Fatal("live legacy daemon was not rejected")
	}
	for _, text := range []string{fmt.Sprintf("PID %d", pid), "systemctl --user disable --now ws-daemon.service", fmt.Sprintf("kill %d", pid)} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("legacy daemon error %q does not contain %q", err, text)
		}
	}
}
