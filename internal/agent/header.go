package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// HeaderCap is the maximum number of chips that share the pinned
// quick-nav header. Nine because chips are numbered 1-9 for direct
// keyboard launch — adding more would need shift+digit and isn't
// worth the cognitive cost.
const HeaderCap = 9

// headerProjects returns the single ordered list of projects rendered
// in the pinned quick-nav chip header. Favorites come first (always
// visible regardless of activity), then non-favorite recently-touched
// projects. The result is capped at HeaderCap so the chips fit in the
// 1-9 number-key hotkey range. Returns nil when nothing qualifies —
// the caller skips header rendering entirely in that case.
func headerProjects(projects []Project) []Project {
	var favs, recent []Project
	for _, p := range projects {
		if p.Favorite {
			favs = append(favs, p)
		} else if !p.LastActiveAt.IsZero() {
			recent = append(recent, p)
		}
	}
	sortByActivity(favs)
	sortByActivity(recent)
	merged := append(favs, recent...)
	return capProjects(merged, HeaderCap)
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

// renderHeaderChips formats `projects` as numbered chips packed into
// at most `maxLines` lines of width `w`. Each chip is rendered as
// `1.name 2m` with a leading `*` for favorites. Chips wrap to a new
// line when the next chip would overflow `w`; chips that would not
// fit in `maxLines` are dropped (HeaderCap=9 already keeps the count
// small enough that this is rare).
//
// Returns nil when projects is empty — callers omit the header rows
// entirely so an idle workspace doesn't burn vertical space on chrome.
func renderHeaderChips(projects []Project, w, maxLines int) []string {
	if len(projects) == 0 || w <= 0 || maxLines <= 0 {
		return nil
	}
	chips := make([]string, len(projects))
	for i, p := range projects {
		chips[i] = formatChip(i+1, p)
	}
	return packChips(chips, w, maxLines)
}

// formatChip builds the "1.name 2m" string for one header project,
// prefixed with `*` when the project is favorited. The age is omitted
// when LastActiveAt is zero (favorited but never stamped).
func formatChip(num int, p Project) string {
	star := ""
	if p.Favorite {
		star = "*"
	}
	age := humanizeAge(p.LastActiveAt)
	if age == "" {
		return fmt.Sprintf("%s%d.%s", star, num, p.Name)
	}
	return fmt.Sprintf("%s%d.%s %s", star, num, p.Name, age)
}

// packChips greedily fills lines with chips separated by two spaces,
// breaking to a new line whenever appending the next chip would push
// the running width past w. Stops once maxLines is reached, dropping
// the remaining chips silently.
func packChips(chips []string, w, maxLines int) []string {
	var lines []string
	cur := ""
	for _, c := range chips {
		next := c
		if cur != "" {
			next = cur + "  " + c
		}
		if lipgloss.Width(next) > w {
			if cur != "" {
				lines = append(lines, cur)
				if len(lines) >= maxLines {
					return lines
				}
			}
			cur = c
			continue
		}
		cur = next
	}
	if cur != "" && len(lines) < maxLines {
		lines = append(lines, cur)
	}
	return lines
}

// styleHeaderLines applies the chip palette to packed header lines:
// favorites get a brighter star, the leading `N.` digit is dimmed so
// the name reads first, and the trailing age column is dim. Operates
// on the raw `1.name 2m`-style strings produced by packChips by
// re-tokenizing on the chip boundary (two spaces). Keep style logic
// confined here so header.go owns the look end-to-end.
func styleHeaderLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = styleChipLine(line)
	}
	return out
}

func styleChipLine(line string) string {
	chips := strings.Split(line, "  ")
	for i, c := range chips {
		chips[i] = styleChip(c)
	}
	return strings.Join(chips, "  ")
}

// styleChip splits one chip into (star?)(N.)(name)( age?) and paints
// each piece. The age separator is a single space; if absent the chip
// ends after the name.
func styleChip(c string) string {
	hasStar := strings.HasPrefix(c, "*")
	if hasStar {
		c = c[1:]
	}
	dot := strings.Index(c, ".")
	if dot < 0 {
		return c
	}
	num := c[:dot]
	rest := c[dot+1:]
	name, age, _ := strings.Cut(rest, " ")

	var b strings.Builder
	if hasStar {
		b.WriteString(favoriteStarStyle.Render("*"))
	}
	b.WriteString(chipNumberStyle.Render(num + "."))
	b.WriteString(chipNameStyle.Render(name))
	if age != "" {
		b.WriteString(" ")
		b.WriteString(activityAgeStyle.Render(age))
	}
	return b.String()
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
