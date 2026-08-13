package syncservice

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func workspace(remote string) *config.Workspace {
	return &config.Workspace{Meta: config.Meta{Version: 1, Root: "/local"}, Groups: map[string]config.Group{}, Aliases: map[string]string{}, Projects: map[string]config.Project{"app": {Remote: remote, Path: "personal/app", Status: config.StatusActive, Mirrors: map[string]string{}, Branches: []config.BranchMeta{{Name: "feat/x", Machines: []string{"linux"}}}}}}
}

func canonical(t *testing.T, ws *config.Workspace) []byte {
	t.Helper()
	b, err := config.EncodeCanonicalWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCanonicalOrderingAndRootExclusion(t *testing.T) {
	a := workspace("origin")
	a.Projects["app"] = config.Project{Remote: "origin", Mirrors: map[string]string{"z": "2", "a": "1"}, Branches: []config.BranchMeta{{Name: "z", Machines: []string{"b", "a", "a"}}, {Name: "a", Machines: []string{"x"}}}}
	b := workspace("origin")
	b.Meta.Root = "/other"
	b.Projects["app"] = config.Project{Remote: "origin", Mirrors: map[string]string{"a": "1", "z": "2"}, Branches: []config.BranchMeta{{Name: "a", Machines: []string{"x"}}, {Name: "z", Machines: []string{"a", "b"}}}}
	ca, cb := canonical(t, a), canonical(t, b)
	if string(ca) != string(cb) {
		t.Fatalf("canonical mismatch\n%s\n%s", ca, cb)
	}
	if strings.Contains(string(ca), "root") {
		t.Fatalf("root persisted: %s", ca)
	}
}

func TestMergeFieldsConflictAndMachineSet(t *testing.T) {
	base := workspace("base")
	local := workspace("local")
	remote := workspace("base")
	p := remote.Projects["app"]
	p.Path = "other"
	p.Branches[0].Machines = []string{"linux", "remote"}
	remote.Projects["app"] = p
	p = local.Projects["app"]
	p.Branches[0].Machines = []string{"linux", "local"}
	local.Projects["app"] = p
	merged, conflicts, err := Merge(base, local, remote)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
	got := merged.Projects["app"]
	if got.Remote != "local" || got.Path != "other" || !reflect.DeepEqual(got.Branches[0].Machines, []string{"linux", "local", "remote"}) {
		t.Fatalf("merged: %+v", got)
	}
	remote = workspace("remote")
	_, conflicts, err = Merge(base, local, remote)
	if err != nil || len(conflicts) != 1 || conflicts[0].Path != "projects.app.remote" {
		t.Fatalf("conflict: %v %+v", err, conflicts)
	}
}

func TestStoreSyncReplayConflictAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "service.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	initial := canonical(t, workspace("base"))
	ref, err := store.AdminImportSingleWorkspace(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	desired := workspace("changed")
	request := SyncRequest{RequestID: "one", WorkspaceID: ref.WorkspaceID, ServiceID: ref.ServiceID, ServiceEpoch: ref.ServiceEpoch, BaseRevision: 1, BaseSemanticHash: ref.SemanticHash, Desired: canonical(t, desired)}
	first, err := store.Sync(ctx, "actor", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.State.Revision != 2 {
		t.Fatalf("revision=%d", first.State.Revision)
	}
	replay, err := store.Sync(ctx, "actor", request)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	request.Desired = initial
	if _, err = store.Sync(ctx, "actor", request); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	current, err := store.Current(ctx, ref.WorkspaceID)
	if err != nil || current.State.Revision != 2 {
		t.Fatalf("reopen: %v %+v", err, current)
	}
}

func TestConcurrentDuplicateRequestWritesOneRevision(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ref, err := store.AdminImportSingleWorkspace(ctx, canonical(t, workspace("base")))
	if err != nil {
		t.Fatal(err)
	}
	request := SyncRequest{RequestID: "same", WorkspaceID: ref.WorkspaceID, ServiceID: ref.ServiceID, ServiceEpoch: ref.ServiceEpoch, BaseRevision: 1, BaseSemanticHash: ref.SemanticHash, Desired: canonical(t, workspace("changed"))}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := store.Sync(ctx, "actor", request); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.Current(ctx, ref.WorkspaceID)
	if err != nil || current.State.Revision != 2 {
		t.Fatalf("current: %v %+v", err, current)
	}
}
