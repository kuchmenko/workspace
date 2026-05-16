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

// buildHeaderChips returns the ordered list of chips rendered in the
// pinned quick-nav. Favorites come first (groups and projects merged,
// sorted by activity desc with name asc tiebreak; groups carry zero
// activity so they sort last among favs), then non-favorite
// recently-touched projects. Capped at HeaderCap so chips fit in the
// 1-9 hotkey range.
func buildHeaderChips(workspaces []WorkspaceData) []Chip {
	var favs, recent []Chip
	for i := range workspaces {
		ws := &workspaces[i]
		for j := range ws.Projects {
			p := &ws.Projects[j]
			c := Chip{
				Kind:          KindProject,
				Name:          p.Name,
				Path:          p.Path,
				Favorite:      p.Favorite,
				LastActiveAt:  p.LastActiveAt,
				Project:       p,
				WorkspaceRoot: ws.Root,
			}
			if p.Favorite {
				favs = append(favs, c)
			} else if !p.LastActiveAt.IsZero() {
				recent = append(recent, c)
			}
		}
		for _, g := range ws.Groups {
			if !ws.FavoriteGroups[g] {
				continue
			}
			favs = append(favs, Chip{
				Kind:          KindGroup,
				Name:          g,
				Path:          GroupPath(ws.Root, g),
				Favorite:      true,
				WorkspaceRoot: ws.Root,
			})
		}
	}
	sortChipsByActivity(favs)
	sortChipsByActivity(recent)
	merged := append(favs, recent...)
	if len(merged) > HeaderCap {
		merged = merged[:HeaderCap]
	}
	return merged
}

func sortChipsByActivity(cs []Chip) {
	sort.Slice(cs, func(i, j int) bool {
		ai, aj := cs[i].LastActiveAt, cs[j].LastActiveAt
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return cs[i].Name < cs[j].Name
	})
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

// renderHeaderChips formats `chips` as numbered hotkey chips packed
// into at most `maxLines` lines of width `w`. Each chip is rendered
// as `1.name 2m` (project) or `1.@group` (group, with `@` prefix to
// disambiguate at a glance). A leading `*` marks favorites. Chips
// that wouldn't fit in `maxLines` are dropped — HeaderCap=9 keeps
// the count small enough that this is rare.
//
// Returns nil on an empty input so callers omit the header rows
// entirely; an idle workspace doesn't burn vertical space on chrome.
func renderHeaderChips(chips []Chip, w, maxLines int) []string {
	if len(chips) == 0 || w <= 0 || maxLines <= 0 {
		return nil
	}
	tokens := make([]string, len(chips))
	for i, c := range chips {
		tokens[i] = formatChip(i+1, c)
	}
	return packChips(tokens, w, maxLines)
}

// formatChip builds the chip token: `*N.name age` for projects and
// `*N.@group` for groups. The age column is omitted when LastActiveAt
// is zero (favorited but never stamped) so chips stay compact on a
// fresh install.
func formatChip(num int, c Chip) string {
	star := ""
	if c.Favorite {
		star = "*"
	}
	body := c.Name
	if c.Kind == KindGroup {
		body = "@" + c.Name
	}
	age := humanizeAge(c.LastActiveAt)
	if age == "" {
		return fmt.Sprintf("%s%d.%s", star, num, body)
	}
	return fmt.Sprintf("%s%d.%s %s", star, num, body, age)
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
