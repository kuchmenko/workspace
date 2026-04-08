// Package pulse aggregates GitHub activity into machine-attributed
// snapshots for `ws pulse`. It is the data layer for the pulse TUI:
// fetch raw events from GitHub, parse them into typed Commit records,
// resolve which machine produced each one (via the wt/<machine>/<topic>
// branch convention or the project.autopush.owned registry), and group
// the result for the UI to render.
//
// Pulse never writes to git or workspace.toml. It is a pure consumer of
// existing state. The TUI in internal/cli/pulse_tui sits on top of the
// types defined here.
package pulse

import "time"

// Source identifies which fetcher produced a given record. Pulse can
// pull from two places at once: the ws GitHub App (always required)
// and the gh CLI (opt-in, used only when gh is installed and logged
// in). Surfacing the source in each record lets the snapshot
// diagnostic show e.g. "kuchmenko/koban came from app, limitless via
// gh", which is the difference between "missing data" and "missing
// installation".
type Source string

const (
	SourceApp Source = "app"  // ws GitHub App, REST + GraphQL via stored OAuth user-to-server token
	SourceGh  Source = "gh"   // local gh CLI shell-out
)

// Period selects how far back to look. Drives the GitHub events query
// (filtered client-side because the events API has no since= parameter)
// and the sparkline bucket resolution.
type Period struct {
	Name    string        // "1d", "7d", "30d" — used as TUI label
	Window  time.Duration // how far back from now we look
	Buckets int           // number of sparkline buckets to render across the window
}

// Standard periods exposed by the TUI hotkeys 1/2/3.
var (
	Period1d  = Period{Name: "1d", Window: 24 * time.Hour, Buckets: 24}
	Period7d  = Period{Name: "7d", Window: 7 * 24 * time.Hour, Buckets: 7}
	Period30d = Period{Name: "30d", Window: 30 * 24 * time.Hour, Buckets: 30}
)

// Commit is a single push event flattened to one commit. One PushEvent
// can fan out into N Commit records (one per SHA in payload.commits).
// Project is the workspace.toml project name (resolved from the GitHub
// repo full_name); empty if the repo is not registered in workspace.toml.
type Commit struct {
	Project   string    // workspace.toml project name; "" if untracked
	Repo      string    // GitHub full_name (owner/repo) — always set
	Branch    string    // short branch name (e.g. "wt/linux/foo" or "feat/x")
	Machine   string    // resolved owning machine; "shared" if unknown
	SHA       string    // short SHA
	Message   string    // first line of commit message
	Author    string    // author email from the event payload
	Timestamp time.Time // event created_at — push time, not commit time
	Source    Source    // which fetcher produced this record
}

// MachineSlice is a sortable slice of Commit aggregated by machine.
// Used by aggregate.go.
type MachineCount struct {
	Machine string
	Count   int
}

// ProjectStat is the per-project rollup that the main pulse view shows.
// The drill-down view also reads Branches and Recent for the second
// page of detail.
type ProjectStat struct {
	Project  string         // workspace.toml name (or repo full_name fallback)
	Repo     string         // owner/repo
	Commits  int            // total commits in window
	Machines []MachineCount // sorted desc; empty if all "shared"
	LastPush time.Time      // most recent commit timestamp
	Spark    []int          // bucketed commit counts, len = Period.Buckets

	// Branches lists each unique branch the user pushed to in this
	// project during the window, sorted by push count desc. Used by
	// the drill view.
	Branches []BranchStat
	// Recent is the most recent N commits (15 by default). The TUI
	// renders this as a "recent pushes" list under the bar chart.
	Recent []Commit
}

// BranchStat is one branch's activity within a project.
type BranchStat struct {
	Branch   string
	Machine  string    // resolved machine for this branch
	Pushes   int       // number of push events
	LastPush time.Time
}

// Snapshot is the full result of one collection pass. Built by
// aggregate.Build from a []Commit. Immutable once returned — the TUI
// caches it until the next refresh.
type Snapshot struct {
	Period      Period
	GeneratedAt time.Time

	TotalCommits int
	Projects     []ProjectStat   // sorted desc by Commits
	ByMachine    []MachineCount  // global breakdown across all projects
	ByDay        []int           // global sparkline, len = Period.Buckets

	// CollectedIn is how long the underlying GitHub fetch + parse +
	// aggregate took. Surfaced in the TUI footer for performance
	// awareness — if this grows past ~1s we know to add caching.
	CollectedIn time.Duration

	// Diagnostic counts for the empty-state view. When a user opens
	// pulse and sees zero commits, these tell them whether GitHub
	// returned events at all (auth/scope problem) or whether the
	// events exist but none are PushEvents in the period (so the
	// answer is "you genuinely have no pushes this week").
	RawEventCount  int
	PushEventCount int

	// Per-source raw event counts. App is always populated; Gh is
	// populated only when the gh CLI was available at fetch time.
	// AppEventCount + GhEventCount may be > RawEventCount because
	// duplicates were merged out.
	AppEventCount int
	GhEventCount  int
	GhAvailable   bool
}
