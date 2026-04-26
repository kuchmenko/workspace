package add

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Source is a producer of Suggestions for the `ws add` TUI. Three
// concrete implementations: DiskSource, ClipboardSource, GitHubSource.
//
// FetchSuggestions must:
//   - Honor ctx cancellation promptly.
//   - Never panic on transient errors — return (nil, err) and let
//     the caller decide whether to surface or swallow.
//   - Return empty slice + nil error when the source has nothing to
//     say (e.g. clipboard doesn't contain a URL). Nil slice is
//     equivalent.
type Source interface {
	// FetchSuggestions returns this source's offerings. AddModel
	// invokes each source in parallel as a separate tea.Cmd; results
	// arrive incrementally and fold into the rendered tree.
	FetchSuggestions(ctx context.Context) ([]Suggestion, error)

	// Name is a short tag for diagnostics ("disk", "clipboard",
	// "github", "gh-cli", etc.). Used in GatherResult to attribute
	// per-source timing and errors.
	Name() string
}

// SourceKind identifies where a Suggestion came from. One Suggestion
// may carry multiple kinds after dedup — e.g. a repo that is both on
// disk AND in the clipboard shows Sources=[Disk, Clipboard].
type SourceKind int

const (
	SourceDisk SourceKind = iota
	SourceClipboard
	SourceGitHub
	SourceManual // typed into the TUI by hand
)

// String returns the short label rendered as a chip in the TUI.
// Keeps the mapping centralized so UI code never hardcodes strings.
func (k SourceKind) String() string {
	switch k {
	case SourceDisk:
		return "disk"
	case SourceClipboard:
		return "clip"
	case SourceGitHub:
		return "gh"
	case SourceManual:
		return "manual"
	default:
		return "?"
	}
}

// Suggestion is one candidate row shown in the `ws add` browse list,
// and also the unit of the dedup layer. Multiple providers surfacing
// the same logical repo merge into one Suggestion with accumulated
// Sources.
type Suggestion struct {
	// Name is the repo short name (e.g. "workspace"). Derived from
	// the URL by the source; may be overridden at register time.
	Name string

	// RemoteURL is the original URL as the source reported it. Use
	// normalizeRemoteURL(RemoteURL) for dedup comparisons; keep the
	// raw string here so register can pass it straight to clone.
	RemoteURL string

	// Sources lists every provider that offered this suggestion. The
	// TUI renders these as chips ([disk] [clip] [gh]).
	Sources []SourceKind

	// DiskPath is non-empty when the suggestion comes from the disk
	// source. Presence flips the register action from "clone" to
	// "migrate / reconcile" because the repo is already local.
	DiskPath string

	// RegisteredPath is non-empty when the GitHub-suggested URL maps
	// to a project already present in workspace.toml. The TUI renders
	// these with a "● cloned at <path>" highlight so the user can tell
	// at a glance which suggestions would be duplicates. Selecting one
	// is still allowed — the edit screen will surface a name conflict
	// and the user can rename to create a copy at a fresh path.
	RegisteredPath string

	// GhActivity is the event count from GitHub Events API — useful
	// for sort order when a repo is in the GitHub source. Zero for
	// non-GitHub suggestions.
	GhActivity int

	// PushedAt is the upstream-last-push timestamp. Zero when the
	// source doesn't provide it (clipboard).
	PushedAt time.Time

	// Description is the human-readable repo blurb, when the source
	// has one. The TUI shows it on the currently-selected row and
	// includes it in the substring search. Empty for clipboard /
	// disk / manual entries (only GitHub provides descriptions
	// today).
	Description string

	// InferredGrp is the group name our grouper assigned. Used by the
	// TUI to pre-fill the group field on the edit screen.
	InferredGrp string
}

// SourceOutcome is the per-source status row tracked by AddModel as
// each Source's FetchSuggestions call completes. Used by the TUI to
// render the "disk:5  github:294" status chip line.
type SourceOutcome struct {
	Name     string
	Count    int           // number of suggestions this source produced
	Duration time.Duration // wall-clock time the fetch took
	Err      error         // nil on success; timeout / failure otherwise
}

// DefaultSourceTimeout is the out-of-the-box per-source deadline.
// AddModel's runTUI raises this to 10s to cover gh-CLI paginate at
// scale; the constant is the lower-bound default for callers that
// don't override.
const DefaultSourceTimeout = 3 * time.Second

// mergeSuggestions deduplicates the union of all source outputs by
// normalized URL. When two providers contribute the same repo, the
// merged Suggestion accumulates Sources from both, and the first
// non-empty field wins for scalars (DiskPath, GhActivity, PushedAt,
// Name, RemoteURL, InferredGrp).
func mergeSuggestions(buckets [][]Suggestion) []Suggestion {
	byKey := make(map[string]*Suggestion)
	for _, bucket := range buckets {
		for _, s := range bucket {
			key := normalizeRemoteURL(s.RemoteURL)
			if key == "" {
				// Fall back to name-based grouping when URL can't be
				// normalized. Better than dropping the entry.
				key = "name:" + s.Name
			}
			cur, ok := byKey[key]
			if !ok {
				copy := s
				byKey[key] = &copy
				continue
			}
			// Merge: union Sources, first non-zero wins for scalars.
			cur.Sources = unionSources(cur.Sources, s.Sources)
			if cur.DiskPath == "" {
				cur.DiskPath = s.DiskPath
			}
			if cur.RegisteredPath == "" {
				cur.RegisteredPath = s.RegisteredPath
			}
			if cur.Description == "" {
				cur.Description = s.Description
			}
			if cur.GhActivity == 0 {
				cur.GhActivity = s.GhActivity
			}
			if cur.PushedAt.IsZero() {
				cur.PushedAt = s.PushedAt
			}
			if cur.Name == "" {
				cur.Name = s.Name
			}
			if cur.RemoteURL == "" {
				cur.RemoteURL = s.RemoteURL
			}
			if cur.InferredGrp == "" {
				cur.InferredGrp = s.InferredGrp
			}
		}
	}

	out := make([]Suggestion, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, *v)
	}
	return out
}

// unionSources appends kinds from b to a that are not already present,
// preserving relative order in a. Expected list sizes are tiny (≤3),
// so a linear search is cheaper than a map.
func unionSources(a, b []SourceKind) []SourceKind {
	out := append([]SourceKind{}, a...)
Outer:
	for _, kb := range b {
		for _, ka := range out {
			if ka == kb {
				continue Outer
			}
		}
		out = append(out, kb)
	}
	return out
}

// sortByRelevance orders merged suggestions so the in-memory slice
// matches the order the TUI tree will render them. Critical: m.cursor
// indexes this slice, and the tree's cursor marker is computed by
// counting items in render order. If the two orderings drift, the
// visual cursor and the actual selection point at different rows
// (the bug observed in production: pressing Enter selected an item
// several rows below the visible ▸).
//
// Sort axes, in descending precedence:
//
//  1. Group order  — Clipboard / Manual at top, then Local
//     (unregistered), then GitHub orgs alphabetical. Mirrors what
//     buildBrowseRows does for headers; pre-sorting here lets that
//     function be a simple linear walk.
//  2. Within group:
//     a. Disk presence (a github repo also-on-disk floats above
//        github-only ones in the same org)
//     b. Activity desc
//     c. PushedAt desc
//     d. Name asc
//
// Stable so that otherwise-equal entries keep the order from the
// first source they appeared in.
func sortByRelevance(s []Suggestion) {
	sort.SliceStable(s, func(i, j int) bool {
		_, li, oi := groupKey(s[i])
		_, lj, oj := groupKey(s[j])
		if oi != oj {
			return oi < oj
		}
		if li != lj {
			return strings.ToLower(li) < strings.ToLower(lj)
		}
		// Same group. Disk presence wins.
		diskI := hasSource(s[i].Sources, SourceDisk)
		diskJ := hasSource(s[j].Sources, SourceDisk)
		if diskI != diskJ {
			return diskI
		}
		if s[i].GhActivity != s[j].GhActivity {
			return s[i].GhActivity > s[j].GhActivity
		}
		if !s[i].PushedAt.Equal(s[j].PushedAt) {
			return s[i].PushedAt.After(s[j].PushedAt)
		}
		return s[i].Name < s[j].Name
	})
}

func hasSource(ss []SourceKind, k SourceKind) bool {
	for _, x := range ss {
		if x == k {
			return true
		}
	}
	return false
}

