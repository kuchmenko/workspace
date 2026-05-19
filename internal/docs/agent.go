// Package docs generates structured documentation from the Cobra command tree.
package docs

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	KeyCapability  = "capability"
	KeyAgentWhen   = "agent:when"
	KeyAgentSafety = "agent:safety"
)

var capabilityMeta = map[string]struct {
	Description string
	Order       int
}{
	"project":       {"Register, clone, and migrate projects", 1},
	"sync":          {"Synchronize workspace state and resolve conflicts", 2},
	"worktree":      {"Create, list, remove, and promote per-feature worktrees", 3},
	"observability": {"Project status and health checks", 4},
	"organization":  {"Shell aliases and project filtering", 5},
	"daemon":        {"Background daemon that auto-syncs projects", 6},
	"auth":          {"GitHub authentication for repo discovery", 7},
	"agent":         {"Launch and manage Claude Code sessions", 8},
}

var constraints = []string{
	"Never run git rebase, reset --hard, or push --force inside a project the daemon is reconciling.",
	"Branches outside the wt/<machine>/* namespace are private and never pushed by the reconciler.",
	"The daemon never runs merge, rebase, reset, or force inside a project repo.",
	"workspace.toml is the single source of truth for project registration — edit it via ws commands, not by hand.",
	"Bare repo directories (*.bare/) must not be modified directly.",
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
		Constraints:  constraints,
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

func SortedCapabilityKeys(m map[string]CapabilityGroup) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, oki := capabilityMeta[keys[i]]
		oj, okj := capabilityMeta[keys[j]]
		if oki && okj {
			return oi.Order < oj.Order
		}
		if oki {
			return true
		}
		if okj {
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
}
