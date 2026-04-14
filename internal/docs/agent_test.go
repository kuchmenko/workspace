package docs_test

import (
	"encoding/json"
	"testing"

	"github.com/kuchmenko/workspace/internal/docs"
	"github.com/spf13/cobra"
)

// newTestTree builds a minimal Cobra command tree with known annotations
// for deterministic testing. It does not depend on the real ws CLI.
func newTestTree() *cobra.Command {
	root := &cobra.Command{
		Use:   "ws",
		Short: "Test workspace manager",
	}

	add := &cobra.Command{
		Use:   "add <remote-url>",
		Short: "Register and clone a new project",
		Annotations: map[string]string{
			docs.KeyCapability:  "project",
			docs.KeyAgentWhen:   "Register a new repo",
			docs.KeyAgentSafety: "Creates a directory",
		},
	}
	add.Flags().String("category", "personal", "project category")
	add.Flags().Bool("no-clone", false, "register without cloning")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show projects",
		Annotations: map[string]string{
			docs.KeyCapability: "project",
			docs.KeyAgentWhen:  "Get an overview of projects",
		},
	}

	// Command without annotations — should be excluded.
	help := &cobra.Command{
		Use:   "help",
		Short: "Help about any command",
	}

	// Hidden command — should be excluded even with annotations.
	hidden := &cobra.Command{
		Use:    "internal",
		Short:  "Internal command",
		Hidden: true,
		Annotations: map[string]string{
			docs.KeyCapability: "project",
			docs.KeyAgentWhen:  "Should not appear",
		},
	}

	// Command in a different capability group.
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Run sync",
		Annotations: map[string]string{
			docs.KeyCapability: "sync",
			docs.KeyAgentWhen:  "Trigger a full sync cycle",
		},
	}

	// Nested subcommand.
	wt := &cobra.Command{
		Use:   "worktree",
		Short: "Manage worktrees",
	}
	wtNew := &cobra.Command{
		Use:   "new <project> <topic>",
		Short: "Create a worktree",
		Annotations: map[string]string{
			docs.KeyCapability: "worktree",
			docs.KeyAgentWhen:  "Start a new feature branch",
		},
	}
	wtNew.Flags().String("from", "", "base ref")
	wtNew.Flags().Bool("auto-push", false, "opt into auto-push")
	wt.AddCommand(wtNew)

	root.AddCommand(add, status, help, hidden, sync, wt)
	return root
}

func TestGenerateAgentCapabilityMap(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	if m.Tool != "ws" {
		t.Errorf("Tool = %q, want %q", m.Tool, "ws")
	}
	if m.Description != "Test workspace manager" {
		t.Errorf("Description = %q, want %q", m.Description, "Test workspace manager")
	}

	// Should have 3 capability groups: project, sync, worktree.
	if len(m.Capabilities) != 3 {
		t.Fatalf("got %d capability groups, want 3: %v",
			len(m.Capabilities), capNames(m.Capabilities))
	}

	// Project group should have 2 commands (add, status).
	proj, ok := m.Capabilities["project"]
	if !ok {
		t.Fatal("missing 'project' capability group")
	}
	if len(proj.Commands) != 2 {
		t.Errorf("project group has %d commands, want 2", len(proj.Commands))
	}

	// Sync group should have 1 command.
	syncGrp, ok := m.Capabilities["sync"]
	if !ok {
		t.Fatal("missing 'sync' capability group")
	}
	if len(syncGrp.Commands) != 1 {
		t.Errorf("sync group has %d commands, want 1", len(syncGrp.Commands))
	}

	// Worktree group should have 1 command (nested under worktree parent).
	wtGrp, ok := m.Capabilities["worktree"]
	if !ok {
		t.Fatal("missing 'worktree' capability group")
	}
	if len(wtGrp.Commands) != 1 {
		t.Errorf("worktree group has %d commands, want 1", len(wtGrp.Commands))
	}

	// Constraints should be non-empty.
	if len(m.Constraints) == 0 {
		t.Error("Constraints is empty")
	}
}

func TestCommandUseIncludesParents(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	wtGrp := m.Capabilities["worktree"]
	if len(wtGrp.Commands) == 0 {
		t.Fatal("no worktree commands")
	}
	cmd := wtGrp.Commands[0]
	want := "ws worktree new <project> <topic>"
	if cmd.Command != want {
		t.Errorf("Command = %q, want %q", cmd.Command, want)
	}
}

func TestFlagsAreCollected(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	proj := m.Capabilities["project"]
	// Find the "add" command.
	var addCmd *docs.AgentCommand
	for i := range proj.Commands {
		if proj.Commands[i].Command == "ws add <remote-url>" {
			addCmd = &proj.Commands[i]
			break
		}
	}
	if addCmd == nil {
		t.Fatal("add command not found in project group")
	}
	if len(addCmd.Flags) != 2 {
		t.Errorf("add has %d flags, want 2: %v", len(addCmd.Flags), addCmd.Flags)
	}
}

func TestSafetyFieldOptional(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	proj := m.Capabilities["project"]
	// "add" has safety, "status" does not.
	for _, cmd := range proj.Commands {
		switch cmd.Command {
		case "ws add <remote-url>":
			if cmd.Safety == "" {
				t.Error("add should have a safety annotation")
			}
		case "ws status":
			if cmd.Safety != "" {
				t.Errorf("status should have no safety, got %q", cmd.Safety)
			}
		}
	}
}

func TestHiddenCommandsExcluded(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	for _, grp := range m.Capabilities {
		for _, cmd := range grp.Commands {
			if cmd.When == "Should not appear" {
				t.Error("hidden command should be excluded from output")
			}
		}
	}
}

func TestUnannotatedCommandsExcluded(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	for _, grp := range m.Capabilities {
		for _, cmd := range grp.Commands {
			if cmd.Command == "ws help" {
				t.Error("unannotated command should be excluded")
			}
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded docs.AgentCapabilityMap
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Tool != m.Tool {
		t.Errorf("round-trip Tool mismatch: %q vs %q", decoded.Tool, m.Tool)
	}
	if len(decoded.Capabilities) != len(m.Capabilities) {
		t.Errorf("round-trip Capabilities count: %d vs %d",
			len(decoded.Capabilities), len(m.Capabilities))
	}
}

func TestSortedCapabilityKeys(t *testing.T) {
	root := newTestTree()
	m := docs.GenerateAgentCapabilityMap(root)

	keys := docs.SortedCapabilityKeys(m.Capabilities)
	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3", len(keys))
	}
	// project (order 1), sync (order 2), worktree (order 3)
	if keys[0] != "project" || keys[1] != "sync" || keys[2] != "worktree" {
		t.Errorf("key order = %v, want [project sync worktree]", keys)
	}
}

func capNames(m map[string]docs.CapabilityGroup) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
