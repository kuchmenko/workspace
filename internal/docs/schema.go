package docs

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
