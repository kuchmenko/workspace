package add

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeSource is a deterministic Source for Gather tests. It can simulate
// a success (fixed list), a failure (err != nil), or a slow-then-done
// behavior (delay > 0) so we can exercise per-source deadlines.
type fakeSource struct {
	name  string
	items []Suggestion
	err   error
	delay time.Duration
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func TestGather_MergesAcrossSources_OneFails(t *testing.T) {
	disk := &fakeSource{
		name: "disk",
		items: []Suggestion{
			{Name: "shared", RemoteURL: "git@github.com:me/shared.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/shared"},
			{Name: "only-disk", RemoteURL: "git@github.com:me/only-disk.git", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/only-disk"},
		},
	}
	clip := &fakeSource{
		name: "clipboard",
		err:  errors.New("no clipboard tool"), // forced failure
	}
	gh := &fakeSource{
		name: "github",
		items: []Suggestion{
			{Name: "shared", RemoteURL: "https://github.com/me/shared", Sources: []SourceKind{SourceGitHub}, GhActivity: 42},
			{Name: "only-gh", RemoteURL: "https://github.com/me/only-gh", Sources: []SourceKind{SourceGitHub}, GhActivity: 10},
		},
	}

	res, err := Gather(context.Background(), []Source{disk, clip, gh}, GatherOptions{})
	if err != nil {
		t.Fatalf("Gather returned error: %v", err)
	}

	// One source failing must not fail the whole gather.
	if got := len(res.Suggestions); got != 3 {
		t.Errorf("merged count: want 3 (shared merged + 2 unique), got %d (%+v)", got, names(res.Suggestions))
	}

	// Per-source outcomes: all three represented.
	if len(res.PerSource) != 3 {
		t.Fatalf("expected 3 per-source outcomes, got %d", len(res.PerSource))
	}

	// The failed source must carry its error in PerSource.
	var clipOutcome *SourceOutcome
	for i := range res.PerSource {
		if res.PerSource[i].Name == "clipboard" {
			clipOutcome = &res.PerSource[i]
		}
	}
	if clipOutcome == nil || clipOutcome.Err == nil {
		t.Errorf("clipboard outcome missing or without error: %+v", clipOutcome)
	}

	// shared appears once, with both chips.
	var shared *Suggestion
	for i := range res.Suggestions {
		if res.Suggestions[i].Name == "shared" {
			shared = &res.Suggestions[i]
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

func TestGather_SortsDiskFirstThenActivity(t *testing.T) {
	src := &fakeSource{
		name: "all",
		items: []Suggestion{
			{Name: "c", RemoteURL: "g@h:a/c", Sources: []SourceKind{SourceGitHub}, GhActivity: 100},
			{Name: "a", RemoteURL: "g@h:a/a", Sources: []SourceKind{SourceDisk}},
			{Name: "b", RemoteURL: "g@h:a/b", Sources: []SourceKind{SourceGitHub}, GhActivity: 50},
		},
	}
	res, _ := Gather(context.Background(), []Source{src}, GatherOptions{})
	want := []string{"a", "c", "b"} // disk wins over activity-100, activity desc
	for i, n := range want {
		if res.Suggestions[i].Name != n {
			t.Errorf("pos %d: want %s, got %s (full: %v)", i, n, res.Suggestions[i].Name, names(res.Suggestions))
		}
	}
}

func TestGather_PerSourceDeadlineDoesNotBlockOthers(t *testing.T) {
	fast := &fakeSource{
		name:  "fast",
		items: []Suggestion{{Name: "quick", RemoteURL: "g@h:a/quick"}},
	}
	slow := &fakeSource{
		name:  "slow",
		delay: 200 * time.Millisecond, // exceeds our test timeout
	}

	start := time.Now()
	res, err := Gather(context.Background(), []Source{fast, slow}, GatherOptions{
		SourceTimeout: 20 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}

	// Must not wait the full 200ms — slow times out at 20ms.
	if elapsed > 150*time.Millisecond {
		t.Errorf("Gather waited %v, expected ≤150ms (slow source should have been cut off)", elapsed)
	}

	// Fast source's result must make it through.
	if len(res.Suggestions) != 1 || res.Suggestions[0].Name != "quick" {
		t.Errorf("unexpected merged: %v", names(res.Suggestions))
	}

	// Slow source's outcome carries a deadline error.
	var slowOut *SourceOutcome
	for i := range res.PerSource {
		if res.PerSource[i].Name == "slow" {
			slowOut = &res.PerSource[i]
		}
	}
	if slowOut == nil || slowOut.Err == nil {
		t.Errorf("slow outcome: %+v", slowOut)
	}
}

func TestGather_EmptySourcesReturnsEmpty(t *testing.T) {
	res, err := Gather(context.Background(), nil, GatherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Suggestions) != 0 {
		t.Errorf("expected empty, got %v", names(res.Suggestions))
	}
	if len(res.PerSource) != 0 {
		t.Errorf("expected empty per-source, got %d entries", len(res.PerSource))
	}
}

func TestGather_CancelledCtxShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := &fakeSource{name: "s"}
	_, err := Gather(ctx, []Source{src}, GatherOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
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

func names(ss []Suggestion) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
