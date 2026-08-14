package syncclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncservice"
)

func testCanonical(t *testing.T, remote string) []byte {
	t.Helper()
	workspace := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Aliases: map[string]string{}, Projects: map[string]config.Project{"app": {Remote: remote, Path: "app", Status: config.StatusActive, Mirrors: map[string]string{}}}}
	canonical, err := config.EncodeCanonicalWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testResponse(t *testing.T, workspaceID, remote string, revision int64) syncservice.SyncResponse {
	t.Helper()
	canonical := testCanonical(t, remote)
	return syncservice.SyncResponse{State: syncservice.StateRef{ServiceID: "service", ServiceEpoch: "epoch", WorkspaceID: workspaceID, Revision: revision, SemanticHash: hash(canonical)}, Workspace: canonical}
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "client.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path
}

func TestCredentialsAtomicPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "client.json")
	want := Credentials{Endpoint: "https://service", ServiceID: "service", ActorID: "actor", ClientKeyPEM: []byte("secret")}
	if err := SaveCredentials(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredentials(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("load: %v %+v", err, got)
	}
	for candidate, mode := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(candidate)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %o", candidate, err, info.Mode().Perm())
		}
	}
}

func TestPairFingerprintMismatchSendsNoToken(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	code := PairingCode{Endpoint: server.URL, ServiceID: "service", CAFingerprint: string(make([]byte, 64)), Token: "must-not-be-sent"}
	if _, err := Pair(context.Background(), code); err == nil {
		t.Fatal("expected fingerprint mismatch")
	}
	if requests.Load() != 0 {
		t.Fatalf("handler received %d requests", requests.Load())
	}
}

func TestPairingCodeRejectsNonOriginEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://service:443", "https://service", "https://service:443/path", "https://user@service:443", "https://service:443?query"} {
		code := PairingCode{Endpoint: endpoint, ServiceID: "service", CAFingerprint: string(make([]byte, 64)), Token: "token"}
		if _, err := ParsePairingCode(code.String()); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
}

func TestCurrentVerifiesWorkspaceCanonicalHash(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*syncservice.SyncResponse)
	}{
		{"workspace", func(response *syncservice.SyncResponse) { response.State.WorkspaceID = "other" }},
		{"hash", func(response *syncservice.SyncResponse) { response.State.SemanticHash = string(make([]byte, 64)) }},
		{"canonical", func(response *syncservice.SyncResponse) { response.Workspace = append(response.Workspace, '\n') }},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := testResponse(t, "workspace", "base", 1)
			test.mutate(&response)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(response) }))
			defer server.Close()
			client := &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
			if _, err := client.Current(context.Background(), "workspace"); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestRemotePullRejectsRevisionRegression(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	root := t.TempDir()
	initial := testResponse(t, "workspace", "base", 2)
	if err := materialize(root, initial.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(ctx, initial); err != nil {
		t.Fatal(err)
	}
	response := testResponse(t, "workspace", "remote", 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(response) }))
	defer server.Close()
	client := &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
	if _, err := client.SyncWorkspace(ctx, store, "workspace", root); err == nil {
		t.Fatal("accepted revision regression")
	}
	current, err := canonicalFile(root)
	if err != nil || !reflect.DeepEqual(current, initial.Workspace) {
		t.Fatalf("local workspace changed: %v", err)
	}
	state, err := store.State(ctx, "workspace")
	if err != nil || state.Phase != "clean" {
		t.Fatalf("state: %v %+v", err, state)
	}
}

func TestPairingAttemptExactRetryAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pair", "attempt.json")
	code := PairingCode{Endpoint: "https://127.0.0.1:1", ServiceID: "service", CAFingerprint: string(make([]byte, 64)), Token: "token"}
	if _, err := PairPersistent(context.Background(), code, "machine", path); err == nil {
		t.Fatal("expected network failure")
	}
	first, err := loadPairingAttempt(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PairPersistent(context.Background(), code, "machine", path); err == nil {
		t.Fatal("expected retry network failure")
	}
	second, err := loadPairingAttempt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("pairing retry changed the persisted attempt")
	}
}

func TestAttachAbsentEqualAndDivergent(t *testing.T) {
	ctx := context.Background()
	response := testResponse(t, "workspace", "base", 1)
	for _, test := range []struct {
		name     string
		local    []byte
		conflict bool
	}{{"absent", nil, false}, {"equal", response.Workspace, false}, {"divergent", testCanonical(t, "local"), true}} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := openTestStore(t)
			root := t.TempDir()
			if test.local != nil {
				if err := materialize(root, test.local); err != nil {
					t.Fatal(err)
				}
			}
			err := store.Attach(ctx, root, response)
			var conflict *BootstrapConflictError
			if errors.As(err, &conflict) != test.conflict {
				t.Fatalf("error=%v", err)
			}
			if test.conflict {
				if got, _ := canonicalFile(root); !reflect.DeepEqual(got, test.local) {
					t.Fatal("divergent file changed")
				}
				return
			}
			state, err := store.State(ctx, "workspace")
			if err != nil || state.Phase != "clean" {
				t.Fatalf("state: %v %+v", err, state)
			}
		})
	}
}

func TestPendingExactRetryAfterReopen(t *testing.T) {
	ctx := context.Background()
	store, path := openTestStore(t)
	root := t.TempDir()
	initial := testResponse(t, "workspace", "base", 1)
	if err := materialize(root, testCanonical(t, "desired")); err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(ctx, initial); err != nil {
		t.Fatal(err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forced", http.StatusInternalServerError) }))
	defer failing.Close()
	client := &Client{credentials: Credentials{Endpoint: failing.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: failing.Client()}
	if _, err := client.SyncWorkspace(ctx, store, "workspace", root); err == nil {
		t.Fatal("expected failure")
	}
	state, err := store.State(ctx, "workspace")
	if err != nil || state.Phase != "pending" {
		t.Fatalf("pending: %v %+v", err, state)
	}
	want := append([]byte(nil), state.Request...)
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var got []byte
	response := testResponse(t, "workspace", "desired", 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = os.ReadFile("/dev/null")
		gotBody := make([]byte, r.ContentLength)
		r.Body.Read(gotBody)
		got = gotBody
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	client = &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
	if _, err = client.SyncWorkspace(ctx, store, "workspace", root); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retry bytes differ\n%s\n%s", got, want)
	}
}

func TestStagedRecoveryConflictAndCAS(t *testing.T) {
	ctx := context.Background()
	t.Run("recovery", func(t *testing.T) {
		store, _ := openTestStore(t)
		root := t.TempDir()
		initial := testResponse(t, "workspace", "base", 1)
		materialize(root, initial.Workspace)
		store.Initialize(ctx, initial)
		request := syncservice.SyncRequest{RequestID: "request", WorkspaceID: "workspace", ServiceID: "service", ServiceEpoch: "epoch", BaseRevision: 1, BaseSemanticHash: initial.State.SemanticHash, Desired: initial.Workspace}
		body, _ := json.Marshal(request)
		store.stagePending(ctx, "workspace", body, initial.Workspace, initial.Workspace, initial.State.SemanticHash)
		response := testResponse(t, "workspace", "service", 2)
		exact, _ := json.Marshal(response)
		store.stageResponse(ctx, "workspace", exact)
		client := &Client{}
		got, err := client.SyncWorkspace(ctx, store, "workspace", root)
		if err != nil || got.State.Revision != 2 {
			t.Fatalf("recover: %v %+v", err, got)
		}
		if current, _ := canonicalFile(root); !reflect.DeepEqual(current, response.Workspace) {
			t.Fatal("response not materialized")
		}
	})
	t.Run("conflict", func(t *testing.T) {
		store, _ := openTestStore(t)
		root := t.TempDir()
		initial := testResponse(t, "workspace", "base", 1)
		materialize(root, initial.Workspace)
		store.Initialize(ctx, initial)
		body, _ := json.Marshal(syncservice.SyncRequest{RequestID: "r"})
		store.stagePending(ctx, "workspace", body, initial.Workspace, testCanonical(t, "client"), initial.State.SemanticHash)
		response := testResponse(t, "workspace", "service", 2)
		response.Conflicts = []syncservice.Conflict{{Path: "projects.app.remote"}}
		exact, _ := json.Marshal(response)
		store.stageResponse(ctx, "workspace", exact)
		client := &Client{}
		_, err := client.SyncWorkspace(ctx, store, "workspace", root)
		if err != nil {
			t.Fatal(err)
		}
		state, _ := store.State(ctx, "workspace")
		if state.Phase != "conflicted" || state.Ref.Revision != 2 {
			t.Fatalf("state=%+v", state)
		}
		if current, _ := canonicalFile(root); !reflect.DeepEqual(current, initial.Workspace) {
			t.Fatal("conflict rewrote file")
		}
	})
	t.Run("cas", func(t *testing.T) {
		store, _ := openTestStore(t)
		root := t.TempDir()
		initial := testResponse(t, "workspace", "base", 1)
		materialize(root, initial.Workspace)
		store.Initialize(ctx, initial)
		body, _ := json.Marshal(syncservice.SyncRequest{RequestID: "r"})
		store.stagePending(ctx, "workspace", body, initial.Workspace, initial.Workspace, initial.State.SemanticHash)
		response := testResponse(t, "workspace", "service", 2)
		exact, _ := json.Marshal(response)
		store.stageResponse(ctx, "workspace", exact)
		concurrent := testCanonical(t, "concurrent")
		materialize(root, concurrent)
		client := &Client{}
		_, err := client.SyncWorkspace(ctx, store, "workspace", root)
		var cas *CASError
		if !errors.As(err, &cas) {
			t.Fatalf("error=%v", err)
		}
		if current, _ := canonicalFile(root); !reflect.DeepEqual(current, concurrent) {
			t.Fatal("CAS overwrote file")
		}
	})
}

func TestIdentityMismatchStagesNoResponse(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	root := t.TempDir()
	initial := testResponse(t, "workspace", "base", 1)
	materialize(root, initial.Workspace)
	store.Initialize(ctx, initial)
	response := testResponse(t, "workspace", "base", 1)
	response.State.ServiceEpoch = "other"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); json.NewEncoder(w).Encode(response) }))
	defer server.Close()
	client := &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
	_, err := client.SyncWorkspace(ctx, store, "workspace", root)
	var mismatch *IdentityError
	if !errors.As(err, &mismatch) || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
	state, _ := store.State(ctx, "workspace")
	if state.Phase != "clean" {
		t.Fatalf("phase=%s", state.Phase)
	}
}

func TestExplicitConflictResolutionUsesObservedServiceState(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	root := t.TempDir()
	initial := testResponse(t, "workspace", "base", 1)
	materialize(root, initial.Workspace)
	store.Initialize(ctx, initial)
	oldDesired := testCanonical(t, "old-desired")
	request, _ := json.Marshal(syncservice.SyncRequest{RequestID: "old"})
	store.stagePending(ctx, "workspace", request, initial.Workspace, oldDesired, hash(oldDesired))
	observed := testResponse(t, "workspace", "service", 2)
	conflict := observed
	conflict.Conflicts = []syncservice.Conflict{{Path: "projects.app.remote"}}
	exact, _ := json.Marshal(conflict)
	store.stageResponse(ctx, "workspace", exact)
	client := &Client{}
	if _, err := client.SyncWorkspace(ctx, store, "workspace", root); err != nil {
		t.Fatal(err)
	}
	state, _ := store.State(ctx, "workspace")
	if state.Ref != observed.State || !reflect.DeepEqual(state.Base, initial.Workspace) || !reflect.DeepEqual(state.Desired, oldDesired) {
		t.Fatalf("conflict state = %+v", state)
	}
	resolved := testCanonical(t, "resolved")
	materialize(root, resolved)
	response := testResponse(t, "workspace", "resolved", 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got syncservice.SyncRequest
		json.NewDecoder(r.Body).Decode(&got)
		if got.BaseRevision != observed.State.Revision || got.BaseSemanticHash != observed.State.SemanticHash || !reflect.DeepEqual(got.Desired, resolved) || got.RequestID == "old" || got.RequestID == "" {
			t.Errorf("resolution request = %+v", got)
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	client = &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
	if _, err := client.ResolveWorkspace(ctx, store, "workspace", root); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State(ctx, "workspace")
	if state.Phase != "clean" || state.Ref != response.State {
		t.Fatalf("resolved state = %+v", state)
	}
}

func TestExplicitResolutionRepersistsConcurrentConflict(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	root := t.TempDir()
	initial := testResponse(t, "workspace", "base", 1)
	materialize(root, initial.Workspace)
	store.Initialize(ctx, initial)
	store.recordConflict(ctx, "workspace", initial.State, initial.Workspace, []syncservice.Conflict{{Path: "old"}})
	concurrent := testResponse(t, "workspace", "concurrent", 2)
	concurrent.Conflicts = []syncservice.Conflict{{Path: "new"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(concurrent) }))
	defer server.Close()
	client := &Client{credentials: Credentials{Endpoint: server.URL, ServiceID: "service", ServiceEpoch: "epoch"}, http: server.Client()}
	response, err := client.ResolveWorkspace(ctx, store, "workspace", root)
	if err != nil || len(response.Conflicts) != 1 {
		t.Fatalf("response: %v %+v", err, response)
	}
	state, _ := store.State(ctx, "workspace")
	if state.Phase != "conflicted" || state.Ref != concurrent.State || len(state.Conflicts) != 1 || state.Conflicts[0].Path != "new" {
		t.Fatalf("conflict state = %+v", state)
	}
}
