package agent

// DemoGraph builds a hardcoded 3-level graph for tests. The cross
// renderer doesn't need a global layout pass, so this just constructs
// the structure.
func DemoGraph() *Graph {
	g := NewGraph(&Node{ID: "ws/dev", Kind: KindWorkspace, Label: "dev"})
	for _, p := range []string{"myapp", "tooling", "notes", "pulse"} {
		g.Add("ws/dev", &Node{ID: "ws/dev/" + p, Kind: KindProject, Label: p})
	}
	return g
}
