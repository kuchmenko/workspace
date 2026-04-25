package add

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Source is a producer of Suggestions. Phase 1-C defines the contract;
// Phase 3 adds the disk / clipboard / github_source implementations.
//
// FetchSuggestions must:
//   - Honor ctx cancellation promptly.
//   - Never panic on transient errors — return (nil, err) and let
//     Gather log + continue.
//   - Return empty slice + nil error when the source has nothing to say
//     (e.g. clipboard doesn't contain a URL). Nil slice is equivalent.
type Source interface {
	// FetchSuggestions returns this source's offerings. Gather invokes
	// each source in parallel with a per-source ctx deadline.
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

	// GhActivity is the event count from GitHub Events API — useful
	// for sort order when a repo is in the GitHub source. Zero for
	// non-GitHub suggestions.
	GhActivity int

	// PushedAt is the upstream-last-push timestamp. Zero when the
	// source doesn't provide it (clipboard).
	PushedAt time.Time

	// InferredGrp is the group name our grouper assigned. Used by the
	// TUI to pre-fill the group field on the edit screen.
	InferredGrp string
}

// GatherResult is the return value of Gather. We keep the per-source
// diagnostics separate from the merged suggestion list so the TUI can
// render accurate chips ("gh: 418ms, 47 repos / clip: 11ms, 0 / disk:
// 342ms, 3") without reconstructing them from the suggestions alone.
type GatherResult struct {
	// Suggestions is the dedup-merged list, sorted by relevance.
	Suggestions []Suggestion

	// PerSource describes each source's outcome. Present for every
	// source Gather was asked to query, even if it returned empty.
	PerSource []SourceOutcome
}

// SourceOutcome is one row in GatherResult.PerSource.
type SourceOutcome struct {
	Name     string
	Count    int           // number of suggestions this source produced
	Duration time.Duration // wall-clock time
	Err      error         // nil on success; timeout/failure otherwise
}

// GatherOptions configures a Gather call. SourceTimeout applies per
// source and never to the aggregate — one slow source should not
// block the others.
type GatherOptions struct {
	// SourceTimeout is the deadline for each individual FetchSuggestions
	// call. 0 → DefaultSourceTimeout.
	SourceTimeout time.Duration
}

// DefaultSourceTimeout is the out-of-the-box per-source deadline. 3s
// is enough for disk walks and typical gh CLI paginations on small
// accounts; Phase 1 sticks with this value because the sole user's
// workspace has <50 repos. Revisit if users at 10× scale start seeing
// timeouts in production (Open item #6 on issue #20).
const DefaultSourceTimeout = 3 * time.Second

// Gather runs all sources in parallel with a per-source ctx deadline,
// merges their results via normalizeRemoteURL-based dedup, and sorts
// by relevance. A source returning an error becomes a chip in
// PerSource — it does NOT cause Gather itself to return an error.
// Gather only returns (nil, err) when ctx itself is already cancelled.
func Gather(ctx context.Context, sources []Source, opts GatherOptions) (*GatherResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := opts.SourceTimeout
	if timeout <= 0 {
		timeout = DefaultSourceTimeout
	}

	outcomes := make([]SourceOutcome, len(sources))
	allRaw := make([][]Suggestion, len(sources))
	var wg sync.WaitGroup

	for i, src := range sources {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			start := time.Now()
			got, err := src.FetchSuggestions(sctx)
			elapsed := time.Since(start)

			outcomes[i] = SourceOutcome{
				Name:     src.Name(),
				Count:    len(got),
				Duration: elapsed,
				Err:      err,
			}
			if err == nil {
				allRaw[i] = got
			}
		}(i, src)
	}
	wg.Wait()

	merged := mergeSuggestions(allRaw)
	sortByRelevance(merged)

	return &GatherResult{
		Suggestions: merged,
		PerSource:   outcomes,
	}, nil
}

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

// sortByRelevance orders merged suggestions: disk-found first (local
// context beats remote), then activity desc, then PushedAt desc, then
// name asc. Stable so that otherwise-equal entries keep the order
// from the first source they appeared in.
func sortByRelevance(s []Suggestion) {
	sort.SliceStable(s, func(i, j int) bool {
		// Disk presence wins.
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

// ErrAllSourcesFailed is returned by GatherResult helper methods when
// every source errored. Gather itself does NOT return this — it lets
// the TUI render whatever partial state it has. Exposed so callers
// that want a stricter contract can check explicitly.
var ErrAllSourcesFailed = errors.New("all suggestion sources failed")
