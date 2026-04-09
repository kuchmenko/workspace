package agent

import (
	"fmt"
)

// GlobalLayout places every node on the canvas grid such that for any
// focused node, its top children sit at the four cardinal cells around
// it. This is a greedy tree-on-grid embedding:
//
//   - Root at (0, 0).
//   - For each node (DFS, children sorted by activity), pick the next
//     available cardinal direction (skipping the one toward the parent)
//     and place the child at that +1 offset. If the cell is occupied by
//     a cousin, that direction is unusable for this node.
//   - Children that don't fit in cardinal cells get folded into a
//     synthetic "…more (N)" portal node, which is itself placed in one
//     of the remaining free cardinal cells. Recursive: a more portal
//     can have its own more portal if its overflow exceeds 3 children.
//
// The result: every parent–child edge in the placed graph is exactly
// one grid cell in a cardinal direction. The cross-navigation interface
// is no longer an overlay — it's literally the 5 cells (focused + 4
// cardinal neighbors) at the center of the camera viewport.
//
// Trade-off: cousins can clash on placement, in which case the loser
// gets bumped to its parent's overflow. Stable across runs because
// children are sorted deterministically and DFS order is fixed.
func GlobalLayout(g *Graph) {
	occupied := make(map[Grid]string)
	placeNodeRecursive(g, g.RootID, Grid{0, 0}, DirNone, occupied)
}

// pendingChild is one child reserved at this node's level, awaiting
// recursion in phase 2.
type pendingChild struct {
	id     string
	pos    Grid
	dir    Direction
}

func placeNodeRecursive(g *Graph, id string, pos Grid, backDir Direction, occupied map[Grid]string) {
	n, ok := g.Nodes[id]
	if !ok {
		return
	}
	n.Pos = pos
	n.Placed = true
	occupied[pos] = id

	free := freeDirectionsOrder(backDir)
	children := MostActiveOrder(g, id)
	if len(children) == 0 {
		return
	}

	// Reserve-one-for-more rule: if children > free, the LAST cardinal
	// direction is reserved for a synthetic more portal so the entire
	// overflow remains reachable via cascading more nodes.
	directChildren := children
	var overflow []string
	if len(children) > len(free) {
		k := len(free) - 1
		if k < 0 {
			k = 0
		}
		directChildren = children[:k]
		overflow = children[k:]
	}

	// PHASE 1: reserve cells for ALL of this node's children first,
	// before recursing into any of them. This prevents cousin
	// recursions from poaching cells that should belong to siblings of
	// the current node.
	var pending []pendingChild
	for _, cid := range directChildren {
		dir, ok := pickFreeDir(pos, free, occupied)
		if !ok {
			overflow = append(overflow, cid)
			continue
		}
		target := pos.Add(directionOffset(dir))
		occupied[target] = cid
		free = removeDir(free, dir)
		pending = append(pending, pendingChild{id: cid, pos: target, dir: dir})
	}

	// Reserve the more portal too, before recursing. Reparent each
	// overflow child so that edges (which follow Parent) connect
	// correctly from the more portal, not from the original parent.
	if len(overflow) > 0 {
		dir, ok := pickFreeDir(pos, free, occupied)
		if ok {
			target := pos.Add(directionOffset(dir))
			moreID := fmt.Sprintf("%s/__more__", id)
			overflowCopy := append([]string(nil), overflow...)
			for _, oid := range overflowCopy {
				if c, ok := g.Nodes[oid]; ok {
					c.Parent = moreID
				}
			}
			moreNode := &Node{
				ID:       moreID,
				Kind:     KindPortal,
				Label:    fmt.Sprintf("…more (%d)", len(overflowCopy)),
				Parent:   id,
				Children: overflowCopy,
			}
			g.Nodes[moreID] = moreNode
			n.Children = append(n.Children, moreID)
			occupied[target] = moreID
			free = removeDir(free, dir)
			pending = append(pending, pendingChild{id: moreID, pos: target, dir: dir})
		}
		// If pickFreeDir failed here, overflow stays unplaced. The
		// reserve-one-for-more guarantees this only happens when cousins
		// from elsewhere have stolen all available cells.
	}

	// PHASE 2: now recurse into each reserved child. By this point all
	// of this parent's slots are claimed in the occupied map, so deeper
	// recursions can't accidentally backtrack into them.
	for _, p := range pending {
		placeNodeRecursive(g, p.id, p.pos, p.dir.Opposite(), occupied)
	}
}

// freeDirectionsOrder returns the cardinal directions in priority order
// (N, E, S, W), excluding the back direction.
func freeDirectionsOrder(backDir Direction) []Direction {
	all := []Direction{DirNorth, DirEast, DirSouth, DirWest}
	if backDir == DirNone {
		return all
	}
	out := make([]Direction, 0, 3)
	for _, d := range all {
		if d != backDir {
			out = append(out, d)
		}
	}
	return out
}

// pickFreeDir returns the first direction in `free` whose +1 offset from
// `pos` is not yet occupied. Returns (DirNone, false) if none fit.
func pickFreeDir(pos Grid, free []Direction, occupied map[Grid]string) (Direction, bool) {
	for _, d := range free {
		target := pos.Add(directionOffset(d))
		if _, taken := occupied[target]; !taken {
			return d, true
		}
	}
	return DirNone, false
}

// removeDir returns free with d removed.
func removeDir(free []Direction, d Direction) []Direction {
	out := make([]Direction, 0, len(free))
	for _, x := range free {
		if x != d {
			out = append(out, x)
		}
	}
	return out
}

// directionOffset returns the +1 grid offset for a direction.
func directionOffset(d Direction) Grid {
	switch d {
	case DirNorth:
		return Grid{0, -1}
	case DirSouth:
		return Grid{0, 1}
	case DirEast:
		return Grid{1, 0}
	case DirWest:
		return Grid{-1, 0}
	}
	return Grid{}
}

// directionFromOffset returns the cardinal direction matching a unit
// offset, or DirNone if the offset is not a unit cardinal step.
func directionFromOffset(off Grid) Direction {
	switch off {
	case Grid{0, -1}:
		return DirNorth
	case Grid{0, 1}:
		return DirSouth
	case Grid{1, 0}:
		return DirEast
	case Grid{-1, 0}:
		return DirWest
	}
	return DirNone
}

// Add returns g shifted by o. (Methods on Grid live alongside the type
// in types.go; redeclared here only if you want to make this file
// self-contained — currently the Grid methods come from types.go.)
