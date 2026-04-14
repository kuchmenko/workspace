package docs

// AgentCapabilityMap is the top-level JSON structure emitted by `ws docs --agent`.
type AgentCapabilityMap struct {
	Tool         string                       `json:"tool"`
	Version      string                       `json:"version,omitempty"`
	Description  string                       `json:"description"`
	Capabilities map[string]CapabilityGroup   `json:"capabilities"`
	Constraints  []string                     `json:"constraints"`
}

// CapabilityGroup clusters related commands under a human-readable label.
type CapabilityGroup struct {
	Description string         `json:"description"`
	Commands    []AgentCommand `json:"commands"`
}

// AgentCommand describes a single CLI invocation an agent can use.
type AgentCommand struct {
	Command string   `json:"command"`
	When    string   `json:"when"`
	Flags   []string `json:"flags,omitempty"`
	Safety  string   `json:"safety,omitempty"`
}
