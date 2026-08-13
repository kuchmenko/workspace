package syncservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func openImportedStore(t *testing.T) (*Store, StateRef) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "private", "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.AdminImportSingleWorkspace(context.Background(), canonical(t, workspace("base")))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, ref
}

func requestFor(t *testing.T, ref StateRef, id, remote string) SyncRequest {
	t.Helper()
	return SyncRequest{RequestID: id, WorkspaceID: ref.WorkspaceID, ServiceID: ref.ServiceID, ServiceEpoch: ref.ServiceEpoch, BaseRevision: ref.Revision, BaseSemanticHash: ref.SemanticHash, Desired: canonical(t, workspace(remote))}
}

func TestStoreIdenticalDesiredDoesNotAdvanceRevision(t *testing.T) {
	store, ref := openImportedStore(t)
	response, err := store.Sync(context.Background(), "actor", requestFor(t, ref, "same", "base"))
	if err != nil || response.State.Revision != ref.Revision || response.State.SemanticHash != ref.SemanticHash {
		t.Fatalf("response: %v %+v", err, response)
	}
}

func TestAdminImportSingleWorkspaceRejectsSecondImport(t *testing.T) {
	store, _ := openImportedStore(t)
	_, err := store.AdminImportSingleWorkspace(context.Background(), canonical(t, workspace("other")))
	if !errors.Is(err, ErrSingleWorkspaceAlreadyImported) {
		t.Fatalf("error = %v", err)
	}
}

func TestAdminImportSingleWorkspaceReturnsExistingRefForSemanticRetry(t *testing.T) {
	store, ref := openImportedStore(t)
	retry, err := store.AdminImportSingleWorkspace(context.Background(), append(canonical(t, workspace("base")), '\n'))
	if err != nil || retry != ref {
		t.Fatalf("retry: %v %+v, want %+v", err, retry, ref)
	}
}

func TestStoreMergesFromRetainedStaleBase(t *testing.T) {
	store, ref := openImportedStore(t)
	firstDesired := workspace("base")
	project := firstDesired.Projects["app"]
	project.Path = "service-path"
	firstDesired.Projects["app"] = project
	first := requestFor(t, ref, "first", "base")
	first.Desired = canonical(t, firstDesired)
	if _, err := store.Sync(context.Background(), "service-change", first); err != nil {
		t.Fatal(err)
	}
	stale := requestFor(t, ref, "stale", "client-remote")
	response, err := store.Sync(context.Background(), "client-change", stale)
	if err != nil || response.State.Revision != 3 {
		t.Fatalf("response: %v %+v", err, response)
	}
	merged, err := configWorkspace(response.Workspace)
	if err != nil || merged.Projects["app"].Remote != "client-remote" || merged.Projects["app"].Path != "service-path" {
		t.Fatalf("merged: %v %+v", err, merged)
	}
}

func TestStoreRejectsUnknownBaseWrongHashAndServiceIdentity(t *testing.T) {
	store, ref := openImportedStore(t)
	tests := []struct {
		name string
		edit func(*SyncRequest)
		want error
	}{
		{"unknown base", func(r *SyncRequest) { r.BaseRevision = 99 }, ErrBaseNotFound},
		{"wrong hash", func(r *SyncRequest) { r.BaseSemanticHash = "wrong" }, ErrStateMismatch},
		{"wrong service", func(r *SyncRequest) { r.ServiceID = "wrong" }, ErrStateMismatch},
		{"missing service", func(r *SyncRequest) { r.ServiceID = "" }, ErrStateMismatch},
		{"wrong epoch", func(r *SyncRequest) { r.ServiceEpoch = "wrong" }, ErrStateMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestFor(t, ref, test.name, "changed")
			test.edit(&request)
			if _, err := store.Sync(context.Background(), "actor", request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestStoreRequestIDIsBoundToServiceID(t *testing.T) {
	store, ref := openImportedStore(t)
	request := requestFor(t, ref, "bound", "changed")
	if _, err := store.Sync(context.Background(), "actor", request); err != nil {
		t.Fatal(err)
	}
	request.ServiceID = "different"
	if _, err := store.Sync(context.Background(), "actor", request); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("error = %v", err)
	}
}

func TestStoreReplayReturnsExactResponseAfterAdvancement(t *testing.T) {
	store, ref := openImportedStore(t)
	request := requestFor(t, ref, "original", "first")
	original, err := store.Sync(context.Background(), "actor", request)
	if err != nil {
		t.Fatal(err)
	}
	advancedRef := original.State
	if _, err := store.Sync(context.Background(), "other", requestFor(t, advancedRef, "advance", "second")); err != nil {
		t.Fatal(err)
	}
	replay, err := store.Sync(context.Background(), "actor", request)
	if err != nil || !reflect.DeepEqual(replay, original) {
		t.Fatalf("replay: %v\n got: %+v\nwant: %+v", err, replay, original)
	}
}

func TestStoreConflictDoesNotAdvanceCanonicalState(t *testing.T) {
	store, ref := openImportedStore(t)
	serviceResponse, err := store.Sync(context.Background(), "service", requestFor(t, ref, "service", "service"))
	if err != nil {
		t.Fatal(err)
	}
	conflicting := requestFor(t, ref, "client", "client")
	response, err := store.Sync(context.Background(), "client", conflicting)
	if err != nil || len(response.Conflicts) != 1 {
		t.Fatalf("conflict response: %v %+v", err, response)
	}
	current, err := store.Current(context.Background(), ref.WorkspaceID)
	if err != nil || !reflect.DeepEqual(current, serviceResponse) {
		t.Fatalf("current changed: %v\n got: %+v\nwant: %+v", err, current, serviceResponse)
	}
}

func TestStoreConcurrentDuplicateRequestReturnsSameResponse(t *testing.T) {
	store, ref := openImportedStore(t)
	request := requestFor(t, ref, "duplicate", "changed")
	responses := make(chan SyncResponse, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := store.Sync(context.Background(), "actor", request)
			responses <- response
			errs <- err
		}()
	}
	wg.Wait()
	close(responses)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []SyncResponse
	for response := range responses {
		got = append(got, response)
	}
	if len(got) != 2 || !reflect.DeepEqual(got[0], got[1]) {
		t.Fatalf("responses: %+v", got)
	}
}

func TestStoreReopenRetainsRevisionsRequestsAndIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "service.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.AdminImportSingleWorkspace(ctx, canonical(t, workspace("base")))
	if err != nil {
		t.Fatal(err)
	}
	request := requestFor(t, ref, "persisted", "changed")
	original, err := store.Sync(ctx, "actor", request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.ServiceRef().ServiceID != ref.ServiceID || store.ServiceRef().ServiceEpoch != ref.ServiceEpoch {
		t.Fatalf("identity changed: %+v", store.ServiceRef())
	}
	replay, err := store.Sync(ctx, "actor", request)
	if err != nil || !reflect.DeepEqual(replay, original) {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	base, err := store.Revision(ctx, ref.WorkspaceID, 1)
	if err != nil || base.State.SemanticHash != ref.SemanticHash {
		t.Fatalf("base: %v %+v", err, base)
	}
}

func TestOpenRestrictsDatabasePermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "db")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "service.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, candidate := range []string{directory, path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if candidate == directory {
			want = 0o700
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", candidate, got, want)
		}
	}
}

func configWorkspace(data []byte) (*config.Workspace, error) {
	return config.DecodeCanonicalWorkspace(data)
}
