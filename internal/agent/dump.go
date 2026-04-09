package agent

import (
	"encoding/json"
)

// DumpNode is the JSON-serializable view of a Node, used by --json mode
// to give the assistant full visibility into layout/highlight state for
// debugging without needing to render to a TTY.
type DumpNode struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Kind     string         `json:"kind"`
	Parent   string         `json:"parent,omitempty"`
	Children []string       `json:"children,omitempty"`
	Pos      Grid           `json:"pos"`
	Placed   bool           `json:"placed"`
	Payload  map[string]string `json:"payload,omitempty"`
}

// DumpSlot is the JSON view of one of the four cardinal slots.
type DumpSlot struct {
	Direction string   `json:"direction"`
	Kind      string   `json:"kind"`
	NodeID    string   `json:"node_id,omitempty"`
	Label     string   `json:"label,omitempty"`
	Children  []string `json:"children,omitempty"`
}

// DumpEdge is a parent→child connection in the laid-out graph.
type DumpEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	FromPos Grid   `json:"from_pos"`
	ToPos   Grid   `json:"to_pos"`
	Dir     string `json:"direction"` // N/E/S/W from parent to child
}

// Dump is the full JSON snapshot of the agent state at construction
// time. Includes the laid-out graph, computed slots for the focused
// node, highlight levels, edges, and viewport metadata.
type Dump struct {
	Focused   string                  `json:"focused"`
	BackDir   string                  `json:"back_dir,omitempty"`
	History   []string                `json:"history,omitempty"`
	Slots     []DumpSlot              `json:"slots"`
	Highlight map[string]string       `json:"highlight"`
	Nodes     map[string]DumpNode     `json:"nodes"`
	Edges     []DumpEdge              `json:"edges"`
	Layout    DumpLayout              `json:"layout"`
}

// DumpLayout records the constants used by the renderer so visual
// expectations can be reproduced from the JSON alone.
type DumpLayout struct {
	CellW int `json:"cell_w"`
	CellH int `json:"cell_h"`
}

// BuildDump returns a JSON-ready snapshot of the model's current state.
func (m *Model) BuildDump() Dump {
	d := Dump{
		Focused:   m.currentID(),
		BackDir:   m.backDirection().String(),
		Highlight: map[string]string{},
		Nodes:     map[string]DumpNode{},
		Layout:    DumpLayout{CellW: cellW, CellH: cellH},
	}
	for _, h := range m.history {
		d.History = append(d.History, h.nodeID)
	}

	slots := m.computeSlots()
	for _, dir := range []Direction{DirNorth, DirEast, DirSouth, DirWest} {
		s := slots[dir]
		d.Slots = append(d.Slots, DumpSlot{
			Direction: dir.String(),
			Kind:      slotKindString(s.Kind),
			NodeID:    s.NodeID,
			Label:     s.Label,
			Children:  s.Children,
		})
	}

	highlight := m.computeHighlight(slots)
	for id, lvl := range highlight {
		d.Highlight[id] = highlightString(lvl)
	}

	for id, n := range m.graph.Nodes {
		d.Nodes[id] = DumpNode{
			ID:       n.ID,
			Label:    n.Label,
			Kind:     kindString(n.Kind),
			Parent:   n.Parent,
			Children: n.Children,
			Pos:      n.Pos,
			Placed:   n.Placed,
			Payload:  n.Payload,
		}
	}

	// Edges: every parent→child pair where both are placed.
	for _, n := range m.graph.Nodes {
		if !n.Placed {
			continue
		}
		for _, cid := range n.Children {
			c, ok := m.graph.Nodes[cid]
			if !ok || !c.Placed {
				continue
			}
			off := Grid{c.Pos.X - n.Pos.X, c.Pos.Y - n.Pos.Y}
			d.Edges = append(d.Edges, DumpEdge{
				From:    n.ID,
				To:      c.ID,
				FromPos: n.Pos,
				ToPos:   c.Pos,
				Dir:     directionFromOffset(off).String(),
			})
		}
	}
	return d
}

// MarshalDump returns the dump as indented JSON bytes.
func (m *Model) MarshalDump() ([]byte, error) {
	return json.MarshalIndent(m.BuildDump(), "", "  ")
}

func slotKindString(k SlotKind) string {
	switch k {
	case SlotEmpty:
		return "empty"
	case SlotBack:
		return "back"
	case SlotChild:
		return "child"
	case SlotMore:
		return "more"
	}
	return "?"
}

func highlightString(l HighlightLevel) string {
	switch l {
	case HLFocused:
		return "focused"
	case HLNavTop:
		return "nav-top"
	case HLNavBack:
		return "nav-back"
	case HLNavMore:
		return "nav-more"
	case HLOverflow:
		return "overflow"
	case HLAncestor:
		return "ancestor"
	case HLBackground:
		return "background"
	}
	return "?"
}

func kindString(k NodeKind) string {
	switch k {
	case KindRoot:
		return "root"
	case KindWorkspace:
		return "workspace"
	case KindGroup:
		return "group"
	case KindProject:
		return "project"
	case KindWorktree:
		return "worktree"
	case KindAction:
		return "action"
	case KindOrphan:
		return "orphan"
	case KindPortal:
		return "portal"
	}
	return "?"
}
