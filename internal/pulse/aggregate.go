package pulse

import (
	"sort"
	"time"
)

// build assembles a Snapshot from a flat slice of commits. Pure
// function: no IO, no globals, deterministic. The TUI calls this
// after each fetch.
func build(commits []Commit, period Period, now time.Time) Snapshot {
	snap := Snapshot{
		Period:       period,
		GeneratedAt:  now,
		TotalCommits: len(commits),
	}

	// Group by project. Project = "" (untracked repo) gets bucketed
	// under its repo full_name so it still shows up.
	type projAgg struct {
		stat        ProjectStat
		machineSet  map[string]int
		bucketSlice []int
		// branchAgg keys by branch name; pulse counts per-branch
		// pushes for the drill-down view's "branches" panel.
		branchAgg map[string]*BranchStat
		// recent collects raw Commit records; trimmed/sorted at the
		// end of aggregation.
		recent []Commit
	}
	bucketStart := now.Add(-period.Window)
	projects := make(map[string]*projAgg)
	machineGlobal := make(map[string]int)
	dayGlobal := make([]int, period.Buckets)

	for _, c := range commits {
		key := c.Project
		if key == "" {
			key = c.Repo
		}
		pa, ok := projects[key]
		if !ok {
			pa = &projAgg{
				stat: ProjectStat{
					Project: key,
					Repo:    c.Repo,
					Spark:   make([]int, period.Buckets),
				},
				machineSet: make(map[string]int),
				branchAgg:  make(map[string]*BranchStat),
			}
			projects[key] = pa
		}
		pa.stat.Commits++
		pa.machineSet[c.Machine]++
		machineGlobal[c.Machine]++
		if c.Timestamp.After(pa.stat.LastPush) {
			pa.stat.LastPush = c.Timestamp
		}

		// Branch breakdown.
		bs, ok := pa.branchAgg[c.Branch]
		if !ok {
			bs = &BranchStat{Branch: c.Branch, Machine: c.Machine}
			pa.branchAgg[c.Branch] = bs
		}
		bs.Pushes++
		if c.Timestamp.After(bs.LastPush) {
			bs.LastPush = c.Timestamp
		}

		// Keep raw record for the recent-pushes panel.
		pa.recent = append(pa.recent, c)

		// Bucket assignment: linear bucket = floor((ts - start) / bucket_size).
		bucket := bucketIndex(c.Timestamp, bucketStart, period)
		if bucket >= 0 && bucket < period.Buckets {
			pa.stat.Spark[bucket]++
			dayGlobal[bucket]++
		}
	}

	const recentLimit = 15

	// Materialize machine + branch + recent per project.
	for _, pa := range projects {
		for m, n := range pa.machineSet {
			pa.stat.Machines = append(pa.stat.Machines, MachineCount{Machine: m, Count: n})
		}
		sort.Slice(pa.stat.Machines, func(i, j int) bool {
			return pa.stat.Machines[i].Count > pa.stat.Machines[j].Count
		})

		for _, bs := range pa.branchAgg {
			pa.stat.Branches = append(pa.stat.Branches, *bs)
		}
		sort.Slice(pa.stat.Branches, func(i, j int) bool {
			if pa.stat.Branches[i].Pushes != pa.stat.Branches[j].Pushes {
				return pa.stat.Branches[i].Pushes > pa.stat.Branches[j].Pushes
			}
			return pa.stat.Branches[i].LastPush.After(pa.stat.Branches[j].LastPush)
		})

		sort.Slice(pa.recent, func(i, j int) bool {
			return pa.recent[i].Timestamp.After(pa.recent[j].Timestamp)
		})
		if len(pa.recent) > recentLimit {
			pa.recent = pa.recent[:recentLimit]
		}
		pa.stat.Recent = pa.recent

		snap.Projects = append(snap.Projects, pa.stat)
	}
	sort.Slice(snap.Projects, func(i, j int) bool {
		if snap.Projects[i].Commits != snap.Projects[j].Commits {
			return snap.Projects[i].Commits > snap.Projects[j].Commits
		}
		return snap.Projects[i].LastPush.After(snap.Projects[j].LastPush)
	})

	// Global machine breakdown.
	for m, n := range machineGlobal {
		snap.ByMachine = append(snap.ByMachine, MachineCount{Machine: m, Count: n})
	}
	sort.Slice(snap.ByMachine, func(i, j int) bool {
		return snap.ByMachine[i].Count > snap.ByMachine[j].Count
	})

	snap.ByDay = dayGlobal
	return snap
}

// bucketIndex maps a timestamp to its bucket position. Returns -1 for
// timestamps outside the window.
func bucketIndex(ts, start time.Time, p Period) int {
	if ts.Before(start) {
		return -1
	}
	bucketSize := p.Window / time.Duration(p.Buckets)
	if bucketSize == 0 {
		return -1
	}
	idx := int(ts.Sub(start) / bucketSize)
	if idx >= p.Buckets {
		idx = p.Buckets - 1
	}
	return idx
}
