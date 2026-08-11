package agent

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func TestBuildHeaderChips_FavoritesFirstThenRecent(t *testing.T) {
	now := time.Now().UTC()
	ws := []WorkspaceData{{
		Root: "/ws",
		Projects: []Project{
			{Name: "fav-old", Favorite: true, LastActiveAt: now.Add(-48 * time.Hour)},
			{Name: "recent-new", Favorite: false, LastActiveAt: now.Add(-5 * time.Minute)},
			{Name: "fav-new", Favorite: true, LastActiveAt: now.Add(-1 * time.Minute)},
			{Name: "stale", Favorite: false, LastActiveAt: time.Time{}},
			{Name: "recent-old", Favorite: false, LastActiveAt: now.Add(-3 * time.Hour)},
		},
	}}

	got := chipNames(buildHeaderChips(ws))
	want := []string{"fav-new", "fav-old", "recent-new", "recent-old"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (favs first by activity desc, then recent by activity desc; zero-activity non-favs excluded)", got, want)
	}
}

func TestBuildHeaderChips_ExcludesFavoriteGroups(t *testing.T) {
	now := time.Now().UTC()
	ws := []WorkspaceData{{
		Root:           "/ws",
		Groups:         []string{"work", "personal"},
		FavoriteGroups: map[string]bool{"work": true},
		Projects: []Project{
			{Name: "active", Favorite: false, LastActiveAt: now.Add(-10 * time.Minute)},
		},
	}}
	chips := buildHeaderChips(ws)
	got := chipNames(chips)
	want := []string{"active"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want project-only quick slots %v", got, want)
	}
	if chips[0].Kind != KindProject {
		t.Errorf("quick slot should be KindProject, got %v", chips[0].Kind)
	}
}

func TestBuildHeaderChips_CappedAtNine(t *testing.T) {
	now := time.Now().UTC()
	var projects []Project
	for i := 0; i < 8; i++ {
		projects = append(projects, Project{
			Name:         "fav-" + formatInt(i),
			Favorite:     true,
			LastActiveAt: now.Add(time.Duration(-i) * time.Hour),
		})
		projects = append(projects, Project{
			Name:         "recent-" + formatInt(i),
			Favorite:     false,
			LastActiveAt: now.Add(time.Duration(-i-100) * time.Hour),
		})
	}
	ws := []WorkspaceData{{Root: "/ws", Projects: projects}}
	got := buildHeaderChips(ws)
	if len(got) != HeaderCap {
		t.Errorf("expected cap of %d, got %d", HeaderCap, len(got))
	}
}

func TestBuildHeaderChips_TiesByName(t *testing.T) {
	t0 := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	ws := []WorkspaceData{{
		Root: "/ws",
		Projects: []Project{
			{Name: "z-app", Favorite: false, LastActiveAt: t0},
			{Name: "a-app", Favorite: false, LastActiveAt: t0},
			{Name: "m-app", Favorite: false, LastActiveAt: t0},
		},
	}}
	got := chipNames(buildHeaderChips(ws))
	want := []string{"a-app", "m-app", "z-app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("equal-activity tie should sort by name asc: got %v, want %v", got, want)
	}
}

func TestQuickSlotsLineShowsNumberedProjectTargets(t *testing.T) {
	m := &Model{headerChips: []Chip{{Name: "expert"}, {Name: "workspace"}, {Name: "exchange-api"}}}
	got := m.quickSlotLines(80)
	want := []string{" Quick access · 1 expert · 2 workspace · 3 exchange-api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("quick slots = %q, want %q", got, want)
	}
}

func TestQuickSlotsWrapWithoutHidingTargets(t *testing.T) {
	m := &Model{headerChips: []Chip{{Name: "expert"}, {Name: "workspace"}, {Name: "exchange-api"}}}
	got := m.quickSlotLines(28)
	joined := strings.Join(got, "\n")
	for _, target := range []string{"1 expert", "2 workspace", "3 exchange-api"} {
		if !strings.Contains(joined, target) {
			t.Fatalf("responsive quick slots hide %q: %q", target, joined)
		}
	}
	for _, line := range got {
		if tui.Width(line) > 28 {
			t.Fatalf("quick slot line width = %d: %q", tui.Width(line), line)
		}
	}
}

func TestHumanizeAgeAt(t *testing.T) {
	t0 := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{30 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{36 * time.Hour, "yday"},
		{3 * 24 * time.Hour, "3d"},
		{10 * 24 * time.Hour, "1w"},
		{60 * 24 * time.Hour, "2mo"},
		{800 * 24 * time.Hour, "2y"},
	}
	for _, tc := range cases {
		got := humanizeAgeAt(t0.Add(-tc.offset), t0)
		if got != tc.want {
			t.Errorf("humanizeAgeAt offset=%v: got %q, want %q", tc.offset, got, tc.want)
		}
	}
	if humanizeAge(time.Time{}) != "" {
		t.Errorf("zero time should produce empty string, not a humanized value")
	}
}

func chipNames(cs []Chip) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}
