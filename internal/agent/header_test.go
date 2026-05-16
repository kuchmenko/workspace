package agent

import (
	"reflect"
	"testing"
	"time"
)

func TestHeaderSections_FavoritesBeforeRecent_FavoritesExcludedFromRecent(t *testing.T) {
	now := time.Now().UTC()
	projects := []Project{
		{Name: "fav-old", Favorite: true, LastActiveAt: now.Add(-48 * time.Hour)},
		{Name: "recent-new", Favorite: false, LastActiveAt: now.Add(-5 * time.Minute)},
		{Name: "fav-new", Favorite: true, LastActiveAt: now.Add(-1 * time.Minute)},
		{Name: "stale", Favorite: false, LastActiveAt: time.Time{}},
		{Name: "recent-old", Favorite: false, LastActiveAt: now.Add(-3 * time.Hour)},
	}

	favs, recent := headerSections(projects)

	gotFav := names(favs)
	wantFav := []string{"fav-new", "fav-old"}
	if !reflect.DeepEqual(gotFav, wantFav) {
		t.Errorf("favorites order: got %v, want %v (descending by activity)", gotFav, wantFav)
	}

	gotRecent := names(recent)
	wantRecent := []string{"recent-new", "recent-old"}
	if !reflect.DeepEqual(gotRecent, wantRecent) {
		t.Errorf("recent order: got %v, want %v", gotRecent, wantRecent)
	}

	// Stale (zero LastActiveAt) must never show up in Recent — and never
	// in Favorites either, because it isn't a favorite.
	for _, p := range append(favs, recent...) {
		if p.Name == "stale" {
			t.Errorf("zero-activity non-favorite %q leaked into header", p.Name)
		}
	}
}

func TestHeaderSections_CappedAtFive(t *testing.T) {
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

	favs, recent := headerSections(projects)
	if len(favs) != HeaderCap {
		t.Errorf("favorites should be capped at %d, got %d", HeaderCap, len(favs))
	}
	if len(recent) != HeaderCap {
		t.Errorf("recent should be capped at %d, got %d", HeaderCap, len(recent))
	}
}

func TestHeaderSections_TiesByName(t *testing.T) {
	t0 := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	projects := []Project{
		{Name: "z-app", Favorite: false, LastActiveAt: t0},
		{Name: "a-app", Favorite: false, LastActiveAt: t0},
		{Name: "m-app", Favorite: false, LastActiveAt: t0},
	}
	_, recent := headerSections(projects)
	got := names(recent)
	want := []string{"a-app", "m-app", "z-app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("equal-activity tie should sort by name asc: got %v, want %v", got, want)
	}
}

func TestHeaderSections_FavoritesIncludeZeroActivity(t *testing.T) {
	now := time.Now().UTC()
	projects := []Project{
		{Name: "fresh-fav", Favorite: true, LastActiveAt: time.Time{}},
		{Name: "old-fav", Favorite: true, LastActiveAt: now.Add(-1 * time.Hour)},
	}
	favs, _ := headerSections(projects)
	got := names(favs)
	// Order: activity desc; zero comes last because nothing is greater
	// than zero in time.After semantics.
	want := []string{"old-fav", "fresh-fav"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("favorites with mixed activity: got %v, want %v", got, want)
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

func names(ps []Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
