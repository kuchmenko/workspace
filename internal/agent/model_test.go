package agent

import (
	"testing"
	"time"
)

func TestRebuildItems_SortsProjectsAndGroupsByActivityDesc(t *testing.T) {
	now := time.Now()
	m := &Model{
		workspaces: []WorkspaceData{{
			Root:   "/ws",
			Groups: []string{"alpha", "beta"},
			Projects: []Project{
				{Name: "z", LastActiveAt: now.Add(-2 * time.Hour)},
				{Name: "a", LastActiveAt: now.Add(-30 * time.Minute)},
				{Name: "m"},
				{Name: "alpha-old", Group: "alpha", LastActiveAt: now.Add(-3 * time.Hour)},
				{Name: "beta-stale", Group: "beta", LastActiveAt: now.Add(-1 * time.Hour)},
				{Name: "beta-fresh", Group: "beta", LastActiveAt: now.Add(-10 * time.Minute)},
			},
		}},
		expanded: map[string]bool{groupKey("/ws", "beta"): true},
	}

	m.rebuildItems()

	var got []string
	for _, it := range m.items {
		switch it.kind {
		case KindProject:
			got = append(got, it.project.Name)
		case KindGroup:
			got = append(got, "@"+it.group)
		}
	}

	// Ungrouped first by activity desc (a, z, then activity-less m),
	// then groups by their freshest project (beta before alpha),
	// with beta expanded into its projects by activity desc.
	want := []string{"a", "z", "m", "@beta", "beta-fresh", "beta-stale", "@alpha"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
