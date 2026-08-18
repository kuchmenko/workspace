package cli

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAgentContractProductionInventory(t *testing.T) {
	contract := GenerateAgentContract(NewRootCmd())
	want := []string{
		"ws add",
		"ws alias add",
		"ws alias install",
		"ws alias list",
		"ws alias rm",
		"ws auth logout",
		"ws doctor",
		"ws migrate",
		"ws path",
		"ws sync",
		"ws workspace create",
		"ws workspace list",
		"ws workspace set-root",
		"ws worktree add",
		"ws worktree list",
		"ws worktree push",
		"ws worktree rm",
	}
	got := make([]string, len(contract.Commands))
	for index, command := range contract.Commands {
		got[index] = command.Canonical
		if command.Capability == "" || command.Interaction == "" || command.Approval == "" || command.Mutation == "" || command.Network == "" || command.Stdout == "" || len(command.ExitCodes) == 0 {
			t.Errorf("%s has incomplete agent metadata: %+v", command.Canonical, command)
		}
		if !reflect.DeepEqual(command.InheritedFlags, []string{"--root"}) {
			t.Errorf("%s inherited flags = %v, want [--root]", command.Canonical, command.InheritedFlags)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent inventory = %v, want %v", got, want)
	}
}

func TestAgentContractAliasesAndSafetyDistinctions(t *testing.T) {
	contract := GenerateAgentContract(NewRootCmd())
	commands := map[string]AgentCommand{}
	for _, command := range contract.Commands {
		commands[command.Canonical] = command
	}
	if !reflect.DeepEqual(commands["ws workspace create"].Aliases, []string{"ws workspace add"}) {
		t.Errorf("workspace create aliases = %v", commands["ws workspace create"].Aliases)
	}
	if !reflect.DeepEqual(commands["ws worktree add"].Aliases, []string{"ws wt add"}) {
		t.Errorf("worktree add aliases = %v", commands["ws worktree add"].Aliases)
	}
	if commands["ws migrate"].Mutation != AgentEffectConditional {
		t.Errorf("migrate mutation = %q, want conditional for --check", commands["ws migrate"].Mutation)
	}
	if commands["ws add"].Interaction != AgentInteractionConditional {
		t.Errorf("add interaction = %q, want conditional", commands["ws add"].Interaction)
	}
	if commands["ws add"].Network != AgentEffectRead || commands["ws doctor"].Network != AgentEffectRead || commands["ws worktree add"].Network != AgentEffectRead {
		t.Errorf("read-only network metadata: add=%q doctor=%q worktree-add=%q", commands["ws add"].Network, commands["ws doctor"].Network, commands["ws worktree add"].Network)
	}
}

func TestAgentContractJSONDeterministic(t *testing.T) {
	root := NewRootCmd()
	first, err := json.MarshalIndent(GenerateAgentContract(root), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.MarshalIndent(GenerateAgentContract(root), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("agent contract JSON is not deterministic")
	}
	var decoded AgentContract
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	if !reflect.DeepEqual(decoded, *GenerateAgentContract(root)) {
		t.Fatal("agent contract JSON round trip changed the schema")
	}
}

func TestZeroArgumentLeavesRejectStrayArguments(t *testing.T) {
	paths := [][]string{
		{"sync"}, {"sync", "resolve"}, {"setup"}, {"status"}, {"scan"},
		{"favorite", "list"}, {"auth", "login"}, {"auth", "logout"}, {"auth", "status"},
		{"alias", "list"}, {"alias", "install"}, {"docs"}, {"create"},
	}
	root := NewRootCmd()
	for _, path := range paths {
		command, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if command.Args == nil {
			t.Errorf("%s has no argument validator", command.CommandPath())
			continue
		}
		if err := command.Args(command, []string{"stray"}); err == nil {
			t.Errorf("%s accepted a stray argument", command.CommandPath())
		}
	}
}
