// Package agent implements the canvas-based TUI launcher for Claude Code
// sessions across all workspaces on this machine.
//
// The visual model is a "cross renderer": at any moment exactly five
// nodes are on screen — the focused node in the center plus four
// cardinal slots (back / most-active children / more-portal). The slot
// arrangement is computed per render from the graph structure and the
// navigation history; there is no global graph layout.
//
// See CLAUDE.md and the chat history in PR for the full design rationale.
package agent

// NodeKind classifies a node for rendering and behavior.
type NodeKind int

const (
	KindRoot NodeKind = iota
	KindWorkspace
	KindGroup
	KindProject
	KindWorktree
	KindAction
	KindOrphan
	KindPortal
)

// Grid is an integer position on the global canvas. GlobalLayout assigns
// it once such that every parent's top children sit at +1 cardinal
// offsets — the navigation cross is then literally the 5 cells around
// the camera viewport center.
type Grid struct {
	X, Y int
}

// Add returns g shifted by o.
func (g Grid) Add(o Grid) Grid { return Grid{g.X + o.X, g.Y + o.Y} }

// Sub returns g - o.
func (g Grid) Sub(o Grid) Grid { return Grid{g.X - o.X, g.Y - o.Y} }

// Node is one element of the agent graph.
type Node struct {
	ID       string
	Kind     NodeKind
	Label    string
	Parent   string
	Children []string
	Pos      Grid
	// Placed reports whether GlobalLayout assigned this node to a grid
	// cell. Overflow children with no free cardinal direction available
	// remain unplaced and are skipped by the renderer (they have no
	// visual position; reachable later via search in layer 5+).
	Placed bool
	// Payload holds kind-specific data: e.g. project name, worktree path,
	// session count. Filled by the source layer.
	Payload map[string]string
}

// Graph is the in-memory model the canvas renders. Lookup by ID is O(1).
type Graph struct {
	Nodes  map[string]*Node
	RootID string
}

// NewGraph returns a graph rooted at the given root node.
func NewGraph(root *Node) *Graph {
	return &Graph{
		Nodes:  map[string]*Node{root.ID: root},
		RootID: root.ID,
	}
}

// Add inserts a child node under parentID. The caller is responsible for
// providing a stable, unique ID.
func (g *Graph) Add(parentID string, n *Node) {
	n.Parent = parentID
	g.Nodes[n.ID] = n
	if p, ok := g.Nodes[parentID]; ok {
		p.Children = append(p.Children, n.ID)
	}
}
