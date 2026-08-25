package runner

import (
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestRenameStoppedDefinitionKeepsTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	target := t.TempDir()
	if err := config.SaveMachineConfig(&config.MachineConfig{Runners: []config.RunnerConfig{{ID: "arch-old", Path: target}}}); err != nil {
		t.Fatal(err)
	}
	if err := RenameDefinition("arch-old", "arch-new"); err != nil {
		t.Fatal(err)
	}
	machine, err := config.LoadMachineConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(machine.Runners) != 1 || machine.Runners[0].ID != "arch-new" || machine.Runners[0].Path != target {
		t.Fatalf("renamed definition = %#v", machine.Runners)
	}
}
