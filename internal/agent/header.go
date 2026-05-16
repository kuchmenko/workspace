package agent

import (
	"sort"
	"time"
)

// HeaderCap is the maximum number of project rows shown in either the
// Favorites or Recent header section. Five per section, total ten —
// chosen as the largest count that fits comfortably above the tree on
// a 24-row terminal without pushing all real projects below the fold.
const HeaderCap = 5

// headerSections returns the two project lists rendered in the
// Favorites/Recent shortcut header above the workspace tree:
//
//   - favs:   projects with Favorite=true, sorted by LastActiveAt
//     desc, name asc for ties. Zero-activity favorites sort last
//     but are still included (the user explicitly pinned them).
//     Capped at HeaderCap.
//   - recent: non-favorite projects with LastActiveAt > zero,
//     sorted the same way. Capped at HeaderCap. Projects that
//     have never been stamped never appear here.
//
// Returns two distinct slices — no project ever appears in both;
// favorites take precedence over recent.
func headerSections(projects []Project) (favs, recent []Project) {
	for _, p := range projects {
		if p.Favorite {
			favs = append(favs, p)
		} else if !p.LastActiveAt.IsZero() {
			recent = append(recent, p)
		}
	}
	sortByActivity(favs)
	sortByActivity(recent)
	favs = capProjects(favs, HeaderCap)
	recent = capProjects(recent, HeaderCap)
	return favs, recent
}

func sortByActivity(ps []Project) {
	sort.Slice(ps, func(i, j int) bool {
		ai, aj := ps[i].LastActiveAt, ps[j].LastActiveAt
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return ps[i].Name < ps[j].Name
	})
}

func capProjects(ps []Project, n int) []Project {
	if len(ps) <= n {
		return ps
	}
	return ps[:n]
}

// allProjects flattens every workspace's Projects into a single slice.
// Header sorting is global across workspaces — a Favorites pin from a
// work workspace and one from a personal workspace appear in the same
// list, ordered purely by activity. The category column on the row
// disambiguates if the user is multi-workspace.
func allProjects(workspaces []WorkspaceData) []Project {
	var out []Project
	for _, ws := range workspaces {
		out = append(out, ws.Projects...)
	}
	return out
}

// humanizeAge returns a short human-readable age for the activity
// column, e.g. "2m", "3h", "yday", "5d", "3w", "2mo", "1y". Returns
// the empty string when t is zero (no activity recorded).
func humanizeAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return humanizeAgeAt(t, time.Now())
}

func humanizeAgeAt(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return formatInt(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return formatInt(int(d.Hours())) + "h"
	case d < 48*time.Hour:
		return "yday"
	case d < 7*24*time.Hour:
		return formatInt(int(d.Hours()/24)) + "d"
	case d < 30*24*time.Hour:
		return formatInt(int(d.Hours()/(24*7))) + "w"
	case d < 365*24*time.Hour:
		return formatInt(int(d.Hours()/(24*30))) + "mo"
	default:
		return formatInt(int(d.Hours()/(24*365))) + "y"
	}
}

// formatInt avoids pulling in fmt for the hot path; the values are
// always small non-negative ints (max ~24-30 in practice).
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
