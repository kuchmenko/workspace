// Package docs generates structured documentation from the Cobra command tree.
package docs

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Annotation keys recognised by the collector. Commands without at least
// "capability" and "agent:when" are excluded from agent output.
const (
	KeyCapability  = "capability"
	KeyAgentWhen   = "agent:when"
	KeyAgentSafety = "agent:safety"
)

// capabilityMeta provides a human-readable description and a sort order
// for each known capability group. Unknown groups are still collected but
// land at the end in alphabetical order.
var capabilityMeta = map[string]struct {
	Description string
	Order       int
}{
	"project":       {"Register, clone, migrate, archive, and restore projects", 1},
	"sync":          {"Synchronize workspace state and resolve conflicts", 2},
	"worktree":      {"Create, list, remove, and promote per-feature worktrees", 3},
	"observability": {"Cross-machine activity dashboard and project status", 4},
	"organisation":  {"Groups, shell aliases, and project filtering", 5},
	"daemon":        {"Background daemon that auto-syncs projects", 6},
	"auth":          {"GitHub authentication for repo discovery", 7},
	"agent":         {"Launch and manage Claude Code sessions", 8},
}

// Constraints are project-wide invariants that any agent operating on a
// ws-managed workspace should respect. They are not tied to individual
// commands, so we keep them as a static list.
var constraints = []string{
	"Never run git rebase, reset --hard, or push --force inside a project the daemon is reconciling.",
	"Branches outside the wt/<machine>/* namespace are private and never pushed by the reconciler.",
	"The daemon never runs merge, rebase, reset, or force inside a project repo.",
	"workspace.toml is the single source of truth for project registration — edit it via ws commands, not by hand.",
	"Bare repo directories (*.bare/) must not be modified directly.",
}

// GenerateAgentCapabilityMap walks the Cobra command tree rooted at root
// and returns a capability map built from command annotations.
//
// Only commands that carry both "capability" and "agent:when" annotations
// are included. Hidden commands are excluded.
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
			desc := cap // fallback for unknown groups
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

// walkCommands visits every command in the tree depth-first.
func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, fn)
	}
}

// fullCommandUse builds the full invocation string,
// e.g. "ws worktree new <project> <topic>".
func fullCommandUse(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c != nil; c = c.Parent() {
		parts = append([]string{c.Use}, parts...)
	}

	result := ""
	for i, p := range parts {
		if i == len(parts)-1 {
			// Leaf: keep the full Use including args.
			if result != "" {
				result += " "
			}
			result += p
		} else {
			// Intermediate: strip args, keep only the command name.
			name := commandName(p)
			if result != "" {
				result += " "
			}
			result += name
		}
	}
	return result
}

// commandName extracts the command name from a Use string (strips args).
func commandName(use string) string {
	for i, c := range use {
		if c == ' ' {
			return use[:i]
		}
	}
	return use
}

// collectFlags returns "--name" strings for every non-hidden,
// non-inherited flag on cmd.
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

// toSortedMap converts the accumulator into the final map, preserving
// the order defined in capabilityMeta. JSON object key order is not
// guaranteed, but the struct fields are ordered for deterministic output
// in tests when marshalled with sorted keys.
func toSortedMap(groups map[string]*CapabilityGroup) map[string]CapabilityGroup {
	out := make(map[string]CapabilityGroup, len(groups))
	for k, v := range groups {
		out[k] = *v
	}
	return out
}

// SortedCapabilityKeys returns capability group names in display order.
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
