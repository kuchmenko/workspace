package pulse

import (
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

// Collect runs one full pulse pass: fetch GitHub events from the App
// and (if available) gh CLI in parallel, merge them, parse against
// the workspace's project list, and aggregate into a Snapshot. All
// IO happens here; build/parse/etc. above are pure.
//
// Returns a Snapshot ready for the TUI to render. CollectedIn is set
// to wall-clock duration so the footer can surface "took 412ms".
func Collect(ws *config.Workspace, period Period) (Snapshot, error) {
	start := time.Now()

	username, err := fetchUsername()
	if err != nil {
		return Snapshot{}, err
	}

	since := start.Add(-period.Window)
	rawEvents, appCount, ghCount, ghOK, err := fetchEventsBoth(username, since)
	if err != nil {
		return Snapshot{}, err
	}

	idx := buildProjectIndex(ws.Projects)
	commits := flattenEvents(rawEvents, idx)
	snap := build(commits, period, start)
	snap.RawEventCount = len(rawEvents)
	snap.PushEventCount = countPushEvents(rawEvents)
	snap.AppEventCount = appCount
	snap.GhEventCount = ghCount
	snap.GhAvailable = ghOK
	snap.CollectedIn = time.Since(start)
	return snap, nil
}

// CollectFromEvents is the same as Collect but takes pre-fetched raw
// events. Used by tests with fixture JSON and by callers who want to
// reuse one fetch across multiple period rebuilds (e.g. period switch
// in the TUI before the next refresh).
func CollectFromEvents(ws *config.Workspace, period Period, events []rawEvent, now time.Time) Snapshot {
	idx := buildProjectIndex(ws.Projects)
	commits := flattenEvents(events, idx)
	snap := build(commits, period, now)
	snap.RawEventCount = len(events)
	snap.PushEventCount = countPushEvents(events)
	return snap
}

func countPushEvents(events []rawEvent) int {
	n := 0
	for _, e := range events {
		if e.Type == "PushEvent" {
			n++
		}
	}
	return n
}
