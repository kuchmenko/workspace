package pulse

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

// fixtureEvents constructs raw GitHub events as JSON strings (the
// shape /users/{}/events actually returns) and unmarshals them so the
// test exercises the real parsing path. Each PushEvent counts as one
// pulse unit because the compact event payload doesn't include the
// commits array.
func fixtureEvents(t *testing.T, now time.Time) []rawEvent {
	t.Helper()
	raw := fmt.Sprintf(`[
	  {"type":"PushEvent","created_at":"%s","actor":{"login":"me"},"repo":{"name":"kuchmenko/workspace"},"payload":{"ref":"refs/heads/wt/linux/foo","head":"aaaa1111","before":"deadbeef","size":2}},
	  {"type":"PushEvent","created_at":"%s","actor":{"login":"me"},"repo":{"name":"kuchmenko/workspace"},"payload":{"ref":"refs/heads/feat/login","head":"cccc3333","before":"deadbeef","size":1}},
	  {"type":"PushEvent","created_at":"%s","actor":{"login":"me"},"repo":{"name":"kuchmenko/workspace"},"payload":{"ref":"refs/heads/main","head":"dddd4444","before":"deadbeef","size":1}},
	  {"type":"PushEvent","created_at":"%s","actor":{"login":"me"},"repo":{"name":"stranger/random"},"payload":{"ref":"refs/heads/wt/asahi/contrib","head":"eeee5555","before":"deadbeef","size":1}},
	  {"type":"WatchEvent","created_at":"%s"}
	]`,
		now.Add(-1*time.Hour).Format(time.RFC3339),
		now.Add(-2*time.Hour).Format(time.RFC3339),
		now.Add(-3*time.Hour).Format(time.RFC3339),
		now.Add(-4*time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	var out []rawEvent
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return out
}

func fixtureWorkspace() *config.Workspace {
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"workspace": {
				Remote: "git@github.com:kuchmenko/workspace.git",
				Path:   "personal/workspace",
				Status: config.StatusActive,
				Autopush: &config.Autopush{
					Owned: []config.OwnedBranch{
						{Branch: "feat/login", Machine: "linux", Since: "2026-04-08T00:00:00Z"},
					},
				},
			},
		},
	}
	return ws
}

func TestParseWtMachine(t *testing.T) {
	cases := map[string]string{
		"wt/linux/foo":         "linux",
		"wt/asahi/auth/refactor": "asahi",
		"wt//x":                "",
		"wt/linux":             "",
		"feat/login":           "",
		"main":                 "",
		"":                     "",
	}
	for in, want := range cases {
		if got := parseWtMachine(in); got != want {
			t.Errorf("parseWtMachine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMachineResolution(t *testing.T) {
	ws := fixtureWorkspace()
	idx := buildProjectIndex(ws.Projects)

	cases := []struct {
		project string
		branch  string
		want    string
	}{
		{"workspace", "wt/linux/foo", "linux"},  // from ref
		{"workspace", "wt/asahi/bar", "asahi"},  // from ref
		{"workspace", "feat/login", "linux"},    // from owned registry
		{"workspace", "main", "shared"},         // unknown
		{"", "main", "shared"},                  // untracked repo
	}
	for _, c := range cases {
		got := idx.resolveMachine(c.project, c.branch)
		if got != c.want {
			t.Errorf("resolveMachine(%q,%q) = %q, want %q", c.project, c.branch, got, c.want)
		}
	}
}

func TestCollectFromEvents_Snapshot(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	ws := fixtureWorkspace()
	events := fixtureEvents(t, now)

	snap := CollectFromEvents(ws, Period7d, events, now)

	// 4 PushEvents = 4 pulse units (compact event payload, 1 unit per push).
	if snap.TotalCommits != 4 {
		t.Errorf("TotalCommits = %d, want 4", snap.TotalCommits)
	}
	// Two projects: "workspace" (3 pushes) and "stranger/random" (1).
	if len(snap.Projects) != 2 {
		t.Fatalf("len(Projects) = %d, want 2", len(snap.Projects))
	}
	top := snap.Projects[0]
	if top.Project != "workspace" {
		t.Errorf("top project = %q, want workspace", top.Project)
	}
	if top.Commits != 3 {
		t.Errorf("workspace pushes = %d, want 3", top.Commits)
	}

	// Machine breakdown for workspace: linux=2 (1 wt/linux + 1 feat/login owned), shared=1 (main).
	machineCounts := map[string]int{}
	for _, m := range top.Machines {
		machineCounts[m.Machine] = m.Count
	}
	if machineCounts["linux"] != 2 {
		t.Errorf("workspace linux count = %d, want 2", machineCounts["linux"])
	}
	if machineCounts["shared"] != 1 {
		t.Errorf("workspace shared count = %d, want 1", machineCounts["shared"])
	}

	// Global ByMachine should include asahi from the stranger repo.
	globalMachines := map[string]int{}
	for _, m := range snap.ByMachine {
		globalMachines[m.Machine] = m.Count
	}
	if globalMachines["asahi"] != 1 {
		t.Errorf("global asahi = %d, want 1", globalMachines["asahi"])
	}
	if globalMachines["linux"] != 2 {
		t.Errorf("global linux = %d, want 2", globalMachines["linux"])
	}

	// All commits within last 4h → all in the most recent bucket(s) of a 7d/7-bucket spark.
	// We don't pin the exact bucket, but the sum across ByDay must equal TotalCommits.
	sum := 0
	for _, b := range snap.ByDay {
		sum += b
	}
	if sum != snap.TotalCommits {
		t.Errorf("ByDay sum = %d, want %d", sum, snap.TotalCommits)
	}
}

func TestBucketIndex(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	start := now.Add(-Period7d.Window)
	cases := []struct {
		ts   time.Time
		want int
	}{
		{now.Add(-1 * time.Hour), 6},      // most recent bucket
		{now.Add(-25 * time.Hour), 5},     // ~1 day ago
		{now.Add(-7*24*time.Hour + time.Hour), 0}, // oldest bucket
		{now.Add(-8 * 24 * time.Hour), -1}, // outside window
	}
	for _, c := range cases {
		got := bucketIndex(c.ts, start, Period7d)
		if got != c.want {
			t.Errorf("bucketIndex(%v) = %d, want %d", c.ts, got, c.want)
		}
	}
}
