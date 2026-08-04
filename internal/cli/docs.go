package cli

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	KeyAgentCapability  = "agent:capability"
	KeyAgentInteraction = "agent:interaction"
	KeyAgentApproval    = "agent:approval"
	KeyAgentMutation    = "agent:mutation"
	KeyAgentNetwork     = "agent:network"
	KeyAgentStdout      = "agent:stdout"
	KeyAgentExitCodes   = "agent:exit-codes"

	AgentInteractionNone        = "none"
	AgentInteractionHeadless    = "headless"
	AgentInteractionConditional = "conditional"
	AgentApprovalNone           = "none"
	AgentApprovalRequired       = "required"
	AgentApprovalConditional    = "conditional"
	AgentEffectNone             = "none"
	AgentEffectRead             = "read"
	AgentEffectWrite            = "write"
	AgentEffectConditional      = "conditional"
)

type AgentContract struct {
	Tool        string         `json:"tool"`
	Description string         `json:"description"`
	Commands    []AgentCommand `json:"commands"`
}

type AgentCommand struct {
	Capability     string   `json:"capability"`
	Canonical      string   `json:"canonical"`
	Usage          string   `json:"usage"`
	Aliases        []string `json:"aliases,omitempty"`
	Deprecated     string   `json:"deprecated,omitempty"`
	LocalFlags     []string `json:"local_flags,omitempty"`
	InheritedFlags []string `json:"inherited_flags,omitempty"`
	Interaction    string   `json:"interaction"`
	Approval       string   `json:"approval"`
	Mutation       string   `json:"mutation"`
	Network        string   `json:"network"`
	Stdout         string   `json:"stdout"`
	ExitCodes      []int    `json:"exit_codes"`
}

func agentAnnotations(capability, interaction, approval, mutation, network, stdout, exitCodes string) map[string]string {
	return map[string]string{
		KeyAgentCapability:  capability,
		KeyAgentInteraction: interaction,
		KeyAgentApproval:    approval,
		KeyAgentMutation:    mutation,
		KeyAgentNetwork:     network,
		KeyAgentStdout:      stdout,
		KeyAgentExitCodes:   exitCodes,
	}
}

func newDocsCmd() *cobra.Command {
	var agent bool
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation from the command tree",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent {
				return runDocsAgent(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.Flags().BoolVar(&agent, "agent", false, "emit JSON contract for approved agent commands")
	return cmd
}

func runDocsAgent(cmd *cobra.Command) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(GenerateAgentContract(cmd.Root()))
}

func GenerateAgentContract(root *cobra.Command) *AgentContract {
	contract := &AgentContract{Tool: root.Name(), Description: root.Short}
	walkCommands(root, func(cmd *cobra.Command) {
		capability := cmd.Annotations[KeyAgentCapability]
		if cmd.Hidden || capability == "" {
			return
		}
		contract.Commands = append(contract.Commands, AgentCommand{
			Capability:     capability,
			Canonical:      canonicalCommand(cmd),
			Usage:          fullCommandUse(cmd),
			Aliases:        commandAliases(cmd),
			Deprecated:     cmd.Deprecated,
			LocalFlags:     collectFlags(cmd.LocalNonPersistentFlags()),
			InheritedFlags: collectFlags(cmd.InheritedFlags()),
			Interaction:    cmd.Annotations[KeyAgentInteraction],
			Approval:       cmd.Annotations[KeyAgentApproval],
			Mutation:       cmd.Annotations[KeyAgentMutation],
			Network:        cmd.Annotations[KeyAgentNetwork],
			Stdout:         cmd.Annotations[KeyAgentStdout],
			ExitCodes:      parseExitCodes(cmd.Annotations[KeyAgentExitCodes]),
		})
	})
	sort.Slice(contract.Commands, func(i, j int) bool {
		return contract.Commands[i].Canonical < contract.Commands[j].Canonical
	})
	return contract
}

func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, fn)
	}
}

func canonicalCommand(cmd *cobra.Command) string {
	var names []string
	for current := cmd; current != nil; current = current.Parent() {
		names = append(names, current.Name())
	}
	for left, right := 0, len(names)-1; left < right; left, right = left+1, right-1 {
		names[left], names[right] = names[right], names[left]
	}
	return strings.Join(names, " ")
}

func fullCommandUse(cmd *cobra.Command) string {
	return canonicalCommand(cmd.Parent()) + " " + cmd.Use
}

func collectFlags(flags *pflag.FlagSet) []string {
	var result []string
	flags.VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden {
			result = append(result, "--"+flag.Name)
		}
	})
	return result
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func commandAliases(cmd *cobra.Command) []string {
	var path []*cobra.Command
	for current := cmd; current != nil; current = current.Parent() {
		path = append(path, current)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	var result []string
	for index, command := range path {
		for _, alias := range command.Aliases {
			names := make([]string, len(path))
			for i, part := range path {
				names[i] = part.Name()
			}
			names[index] = alias
			result = append(result, strings.Join(names, " "))
		}
	}
	return sortedCopy(result)
}

func parseExitCodes(value string) []int {
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		code, err := strconv.Atoi(part)
		if err == nil {
			result = append(result, code)
		}
	}
	return result
}
