package agent

import "sort"

// SlotKind classifies the role of one of the four cardinal positions
// around a focused node.
type SlotKind int

const (
	SlotEmpty SlotKind = iota
	SlotBack
	SlotChild
	SlotMore
)

// Slot is one of the four cardinal positions around a focused node.
type Slot struct {
	Kind     SlotKind
	NodeID   string
	Children []string // populated for SlotMore (overflow IDs)
	Label    string
}

// SlotMap is the four-way slot assignment for a focused node.
type SlotMap map[Direction]Slot

// MostActiveOrder returns the children of `focused` ordered by activity
// (most active first). Currently alphabetical; layer 5 will plug in
// pin order + session mtime + algorithm.
func MostActiveOrder(g *Graph, focusedID string) []string {
	n, ok := g.Nodes[focusedID]
	if !ok {
		return nil
	}
	out := append([]string(nil), n.Children...)
	sort.Strings(out)
	return out
}

// ComputeSlots reads the slot map for a focused node directly from the
// graph's grid layout: for each child whose Pos is exactly +1 cardinal
// offset from focused, that child fills the corresponding slot. The
// back slot is filled from the navigation history.
//
// This function assumes GlobalLayout has already run.
func ComputeSlots(g *Graph, focusedID string, backDir Direction, backNodeID string) SlotMap {
	slots := SlotMap{
		DirNorth: {Kind: SlotEmpty},
		DirEast:  {Kind: SlotEmpty},
		DirSouth: {Kind: SlotEmpty},
		DirWest:  {Kind: SlotEmpty},
	}

	focused, ok := g.Nodes[focusedID]
	if !ok {
		return slots
	}

	// Back slot from history.
	if backDir != DirNone && backNodeID != "" {
		slots[backDir] = Slot{
			Kind:   SlotBack,
			NodeID: backNodeID,
			Label:  labelOf(g, backNodeID),
		}
	}

	// Each child fills its cardinal slot based on its actual grid Pos.
	// Children placed by GlobalLayout are at exactly +1 cardinal offset.
	for _, cid := range focused.Children {
		c, ok := g.Nodes[cid]
		if !ok {
			continue
		}
		off := c.Pos.Sub(focused.Pos)
		d := directionFromOffset(off)
		if d == DirNone {
			continue
		}
		if slots[d].Kind != SlotEmpty {
			continue
		}
		kind := SlotChild
		if c.Kind == KindPortal {
			kind = SlotMore
		}
		slots[d] = Slot{
			Kind:     kind,
			NodeID:   cid,
			Label:    c.Label,
			Children: append([]string(nil), c.Children...),
		}
	}

	return slots
}

func labelOf(g *Graph, id string) string {
	if n, ok := g.Nodes[id]; ok {
		if n.Label != "" {
			return n.Label
		}
		return n.ID
	}
	return id
}
