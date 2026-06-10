package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/config"
)

func TestRepairDuplicatedBranchKeysSplitsMissingBranchTable(t *testing.T) {
	input := `
[meta]
version = 1
root = "/tmp/ws"

[daemon]
poll_interval = "5m"
stale_threshold = "30d"
auto_sync = true
watch_dirs = true

[projects.app]
remote = "git@example.com:app.git"
path = "personal/app"
status = "active"
category = "personal"

[[projects.app.branches]]
  name = "main"
  machines = ["linux"]
  last_active_machine = "linux"
  last_active_at = "2026-05-28T05:36:49Z"
  name = "feat/stp"
  machines = ["archlinux"]
  last_active_machine = "archlinux"
  last_active_at = "2026-05-28T12:08:22Z"
  created_by = "archlinux"
  created_at = "2026-05-28T12:08:22Z"
`
	out, changed := repairDuplicatedBranchKeys(input)
	if !changed {
		t.Fatal("expected repair to change input")
	}
	if got := strings.Count(out, "[[projects.app.branches]]"); got != 2 {
		t.Fatalf("expected 2 branch tables, got %d:\n%s", got, out)
	}
}

func TestRepairDuplicatedBranchKeysMergesDuplicatedMetadata(t *testing.T) {
	input := `
[projects.app]
remote = "git@example.com:app.git"
path = "personal/app"
status = "active"
category = "personal"

[[projects.app.branches]]
  name = "master"
  machines = ["archlinux", "linux"]
  last_active_machine = "linux"
  last_active_at = "2026-05-29T03:40:48Z"
  machines = ["archlinux"]
  last_active_machine = "archlinux"
  last_active_at = "2026-05-26T07:16:25Z"
`
	out, changed := repairDuplicatedBranchKeys(input)
	if !changed {
		t.Fatal("expected repair to change input")
	}
	for _, needle := range []string{
		`machines = ["archlinux", "linux"]`,
		`last_active_machine = "archlinux"`,
		`last_active_at = "2026-05-26T07:16:25Z"`,
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("repaired output missing %q:\n%s", needle, out)
		}
	}
	if strings.Count(out, "machines =") != 1 {
		t.Fatalf("duplicate machines still present:\n%s", out)
	}
}

func TestRepairWorkspaceTOMLFixesParsableConfig(t *testing.T) {
	root := t.TempDir()
	broken := `
[meta]
version = 1
root = "/tmp/ws"

[daemon]
poll_interval = "5m"
stale_threshold = "30d"
auto_sync = true
watch_dirs = true

[projects.app]
remote = "git@example.com:app.git"
path = "personal/app"
status = "active"
category = "personal"

[[projects.app.branches]]
  name = "main"
  machines = ["linux"]
  name = "feat/stp"
  machines = ["archlinux"]

[[projects.app.branches]]
  name = "master"
  machines = ["linux"]
  last_active_machine = "linux"
  machines = ["archlinux"]
  last_active_machine = "archlinux"
`
	path := filepath.Join(root, "workspace.toml")
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err == nil {
		t.Fatal("setup should be invalid TOML")
	}
	if err := repairWorkspaceTOML(root); err != nil {
		t.Fatalf("repairWorkspaceTOML: %v", err)
	}
	ws, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load after repair: %v", err)
	}
	branches := ws.Projects["app"].Branches
	if len(branches) != 3 || branches[0].Name != "main" || branches[1].Name != "feat/stp" || branches[2].Name != "master" {
		t.Fatalf("unexpected branches: %+v", branches)
	}
	if len(branches[2].Machines) != 2 {
		t.Fatalf("master machines were not merged: %+v", branches[2])
	}
	if _, err := os.Stat(path + ".doctor-bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestCheckConfigParseErrorIsAutoFixable(t *testing.T) {
	finding := checkConfig(t.TempDir(), nil, os.ErrInvalid)
	if finding.Fix == nil {
		t.Fatal("parse error should be auto-fixable")
	}
	if finding.Severity != Error {
		t.Fatalf("expected Error severity, got %s", finding.Severity)
	}
}
