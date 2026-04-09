package agent

import "testing"

// Helper: build graph and lay it out.
func buildLaidOut(rootID string, children []string) *Graph {
	g := NewGraph(&Node{ID: rootID, Label: rootID})
	for _, c := range children {
		g.Add(rootID, &Node{ID: c, Label: c})
	}
	GlobalLayout(g)
	return g
}

// Tests that root with 3 children at root has them placed in N/E/S
// (priority-order direction picking, no back to skip).
func TestLayoutPlacesRootChildrenCardinally(t *testing.T) {
	g := buildLaidOut("root", []string{"alpha", "bravo", "charlie"})
	want := map[string]Grid{
		"root":    {0, 0},
		"alpha":   {0, -1}, // N (slot 0 in priority N→E→S→W)
		"bravo":   {1, 0},  // E
		"charlie": {0, 1},  // S
	}
	for id, pos := range want {
		got := g.Nodes[id].Pos
		if got != pos {
			t.Errorf("%s: got %+v, want %+v", id, got, pos)
		}
	}
}

// Tests that ComputeSlots reads child positions and assigns each to its
// matching cardinal slot.
func TestComputeSlotsAtRoot(t *testing.T) {
	g := buildLaidOut("root", []string{"alpha", "bravo", "charlie"})
	slots := ComputeSlots(g, "root", DirNone, "")

	cases := map[Direction]string{
		DirNorth: "alpha",
		DirEast:  "bravo",
		DirSouth: "charlie",
	}
	for d, want := range cases {
		s := slots[d]
		if s.Kind != SlotChild || s.NodeID != want {
			t.Errorf("dir %v: want SlotChild %s, got %+v", d, want, s)
		}
	}
	if slots[DirWest].Kind != SlotEmpty {
		t.Errorf("DirWest should be empty: %+v", slots[DirWest])
	}
}

// reserve-one-for-more rule: when children > free dirs, the LAST free
// dir is reserved for a synthetic more portal so all overflow remains
// reachable via cascading more chains.
func TestLayoutReservesMorePortal(t *testing.T) {
	// Root with 5 children, 4 free dirs → 3 direct + 1 more (containing 2).
	g := buildLaidOut("root", []string{"a", "b", "c", "d", "e"})
	more, ok := g.Nodes["root/__more__"]
	if !ok {
		t.Fatalf("expected more portal at root, none created")
	}
	if len(more.Children) != 2 {
		t.Errorf("more should contain 2 overflow children, got %d", len(more.Children))
	}
	if !more.Placed {
		t.Errorf("more portal must be placed in a cardinal cell")
	}
	// All 5 originals must be reachable (placed somewhere — either as
	// cardinal children of root or as cardinal children of the more
	// portal).
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		n, ok := g.Nodes[name]
		if !ok || !n.Placed {
			t.Errorf("child %s should be placed via reserve-one-for-more", name)
		}
	}
	// 3 direct children of root: the first 3 alphabetical (a, b, c).
	for _, name := range []string{"a", "b", "c"} {
		if g.Nodes[name].Parent != "root" {
			t.Errorf("%s should be direct child of root, got parent=%s", name, g.Nodes[name].Parent)
		}
	}
}

// Cascading more portals: 30 children at root yields a chain of more
// nodes such that every original child is reachable.
func TestLayoutCascadingMore(t *testing.T) {
	names := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		names = append(names, string(rune('a'+i%26))+string(rune('0'+i/10)))
	}
	g := buildLaidOut("root", names)
	// Every original child must be placed (either directly or under a
	// more portal).
	for _, name := range names {
		n, ok := g.Nodes[name]
		if !ok {
			t.Errorf("child %s missing from graph", name)
			continue
		}
		if !n.Placed {
			t.Errorf("child %s should be placed via cascading more", name)
		}
	}
}

// Tests that the back slot is filled from history regardless of layout.
func TestComputeSlotsBack(t *testing.T) {
	g := buildLaidOut("root", []string{"X"})
	slots := ComputeSlots(g, "X", DirNorth, "root")
	if slots[DirNorth].Kind != SlotBack || slots[DirNorth].NodeID != "root" {
		t.Errorf("back slot wrong: %+v", slots[DirNorth])
	}
}

// Tests Direction.Opposite roundtrip.
func TestDirectionOpposite(t *testing.T) {
	cases := []struct {
		in, want Direction
	}{
		{DirNorth, DirSouth},
		{DirSouth, DirNorth},
		{DirEast, DirWest},
		{DirWest, DirEast},
		{DirNone, DirNone},
	}
	for _, tc := range cases {
		if got := tc.in.Opposite(); got != tc.want {
			t.Errorf("%v.Opposite(): got %v, want %v", tc.in, got, tc.want)
		}
	}
}
