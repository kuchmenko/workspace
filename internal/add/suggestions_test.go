package add

import (
	"testing"
)

// TestMergeSuggestions_AcrossSources covers the dedup + accumulation
// invariant that AddModel relies on whenever a new source's results
// arrive: the same logical repo discovered in two providers folds
// into one row with both source chips, and the first non-empty
// scalar (DiskPath / GhActivity / etc) wins.
//
// Same shape as the production streaming flow, where each
// sourceDoneMsg triggers mergeSuggestions(allSoFar, justArrived).
func TestMergeSuggestions_AcrossSources(t *testing.T) {
	disk := []Suggestion{
		{Name: "shared", RemoteURL: "git@github.com:me/shared.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/shared"},
		{Name: "only-disk", RemoteURL: "git@github.com:me/only-disk.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/only-disk"},
	}
	gh := []Suggestion{
		// Same logical repo as "shared" via a different URL form —
		// must fold via normalizeRemoteURL into one entry.
		{Name: "shared", RemoteURL: "https://github.com/me/shared", Sources: []SourceKind{SourceGitHub}, GhActivity: 42},
		{Name: "only-gh", RemoteURL: "https://github.com/me/only-gh", Sources: []SourceKind{SourceGitHub}, GhActivity: 10},
	}

	merged := mergeSuggestions([][]Suggestion{disk, gh})

	if got := len(merged); got != 3 {
		t.Errorf("merged count: want 3 (shared merged + 2 unique), got %d (%+v)", got, sugNames(merged))
	}

	var shared *Suggestion
	for i := range merged {
		if merged[i].Name == "shared" {
			shared = &merged[i]
		}
	}
	if shared == nil {
		t.Fatal("shared missing from merged list")
	}
	if !hasSource(shared.Sources, SourceDisk) || !hasSource(shared.Sources, SourceGitHub) {
		t.Errorf("shared.Sources missing disk or gh: %v", shared.Sources)
	}
	if shared.DiskPath == "" {
		t.Error("shared should retain DiskPath from the disk source")
	}
	if shared.GhActivity != 42 {
		t.Errorf("shared should retain GhActivity from gh source, got %d", shared.GhActivity)
	}
}

func TestMergeSuggestions_EmptyInputsReturnEmpty(t *testing.T) {
	merged := mergeSuggestions(nil)
	if len(merged) != 0 {
		t.Errorf("nil input: got %d", len(merged))
	}
	merged = mergeSuggestions([][]Suggestion{nil, {}, nil})
	if len(merged) != 0 {
		t.Errorf("empty arrays: got %d", len(merged))
	}
}

func TestSortByRelevance_DiskFirstWithinGroup(t *testing.T) {
	// Same group (github org "me"), different sources. Disk presence
	// must float to the top within the group; activity is the
	// tiebreaker among github-only entries.
	view := []Suggestion{
		{Name: "c", RemoteURL: "g@h:me/c", Sources: []SourceKind{SourceGitHub}, GhActivity: 100, InferredGrp: "me"},
		{Name: "a", RemoteURL: "g@h:me/a", Sources: []SourceKind{SourceDisk, SourceGitHub}, InferredGrp: "me"},
		{Name: "b", RemoteURL: "g@h:me/b", Sources: []SourceKind{SourceGitHub}, GhActivity: 50, InferredGrp: "me"},
	}
	sortByRelevance(view)
	want := []string{"a", "c", "b"}
	for i, n := range want {
		if view[i].Name != n {
			t.Errorf("pos %d: want %s, got %s (full: %v)", i, n, view[i].Name, sugNames(view))
		}
	}
}

func TestSourceKind_String(t *testing.T) {
	cases := map[SourceKind]string{
		SourceDisk:      "disk",
		SourceClipboard: "clip",
		SourceGitHub:    "gh",
		SourceManual:    "manual",
	}
	for k, want := range cases {
		if k.String() != want {
			t.Errorf("%d.String() = %q, want %q", k, k.String(), want)
		}
	}
}

func TestUnionSources_Idempotent(t *testing.T) {
	a := []SourceKind{SourceDisk, SourceGitHub}
	b := []SourceKind{SourceGitHub, SourceClipboard}
	got := unionSources(a, b)
	wantCount := 3
	if len(got) != wantCount {
		t.Errorf("union: want %d kinds, got %d (%v)", wantCount, len(got), got)
	}
	// Original order preserved, new kinds appended.
	if got[0] != SourceDisk || got[1] != SourceGitHub || got[2] != SourceClipboard {
		t.Errorf("unexpected order: %v", got)
	}
}

// sugNames is a local helper. The other test files (disk_test.go,
// tree_test.go) define their own suggestionNames / names functions
// for the same purpose; keeping a third name avoids collisions
// without restructuring fixture sharing.
func sugNames(ss []Suggestion) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
