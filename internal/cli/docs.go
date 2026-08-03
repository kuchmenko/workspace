package cli

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	KeyCapability  = "capability"
	KeyAgentWhen   = "agent:when"
	KeyAgentSafety = "agent:safety"
)

type AgentCapabilityMap struct {
	Tool         string                     `json:"tool"`
	Version      string                     `json:"version,omitempty"`
	Description  string                     `json:"description"`
	Capabilities map[string]CapabilityGroup `json:"capabilities"`
	Constraints  []string                   `json:"constraints"`
}

type CapabilityGroup struct {
	Description string         `json:"description"`
	Commands    []AgentCommand `json:"commands"`
}

type AgentCommand struct {
	Command string   `json:"command"`
	When    string   `json:"when"`
	Flags   []string `json:"flags,omitempty"`
	Safety  string   `json:"safety,omitempty"`
}

var capabilityMeta = map[string]struct {
	Description string
	Order       int
}{
	"project":       {"Register, clone, and migrate projects", 1},
	"sync":          {"Synchronize workspace state and resolve conflicts", 2},
	"worktree":      {"Create, list, remove, and push per-feature worktrees", 3},
	"observability": {"Project status and health checks", 4},
	"organization":  {"Workspace discovery, shell aliases, and project filtering", 5},
	"auth":          {"GitHub authentication for repo discovery", 6},
	"agent":         {"Launch and manage Claude Code sessions", 7},
}

var agentDocConstraints = []string{
	"Never run git rebase, reset --hard, or push --force inside a project while ws sync is running.",
	"Project branches are never pushed to origin by ws sync; configured mirror pushes are selected sync targets.",
	"ws sync never runs merge, project rebase, reset, force, branch deletion, or origin branch push inside a project repo.",
	"workspace.toml is the single source of truth for project registration — edit it via ws commands, not by hand.",
	"Bare repo directories (*.bare/) must not be modified directly.",
}

func newDocsCmd() *cobra.Command {
	var agent bool

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation from the command tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent {
				return runDocsAgent(cmd)
			}
			return cmd.Help()
		},
	}

	cmd.Flags().BoolVar(&agent, "agent", false, "emit JSON capability map for AI agents")
	return cmd
}

func runDocsAgent(cmd *cobra.Command) error {
	m := GenerateAgentCapabilityMap(cmd.Root())

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}

func GenerateAgentCapabilityMap(root *cobra.Command) *AgentCapabilityMap {
	groups := map[string]*CapabilityGroup{}

	walkCommands(root, func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		cap := cmd.Annotations[KeyCapability]
		when := cmd.Annotations[KeyAgentWhen]
		if cap == "" || when == "" {
			return
		}

		grp, ok := groups[cap]
		if !ok {
			desc := cap
			if meta, known := capabilityMeta[cap]; known {
				desc = meta.Description
			}
			grp = &CapabilityGroup{Description: desc}
			groups[cap] = grp
		}

		ac := AgentCommand{
			Command: fullCommandUse(cmd),
			When:    when,
			Flags:   collectFlags(cmd),
			Safety:  cmd.Annotations[KeyAgentSafety],
		}
		grp.Commands = append(grp.Commands, ac)
	})

	return &AgentCapabilityMap{
		Tool:         "ws",
		Description:  root.Short,
		Capabilities: toSortedMap(groups),
		Constraints:  agentDocConstraints,
	}
}

func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, fn)
	}
}

func fullCommandUse(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c != nil; c = c.Parent() {
		parts = append([]string{c.Use}, parts...)
	}

	result := ""
	for i, p := range parts {
		if i == len(parts)-1 {
			if result != "" {
				result += " "
			}
			result += p
		} else {
			name := commandName(p)
			if result != "" {
				result += " "
			}
			result += name
		}
	}
	return result
}

func commandName(use string) string {
	for i, c := range use {
		if c == ' ' {
			return use[:i]
		}
	}
	return use
}

func collectFlags(cmd *cobra.Command) []string {
	var out []string
	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		out = append(out, "--"+f.Name)
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func toSortedMap(groups map[string]*CapabilityGroup) map[string]CapabilityGroup {
	out := make(map[string]CapabilityGroup, len(groups))
	for k, v := range groups {
		out[k] = *v
	}
	return out
}
