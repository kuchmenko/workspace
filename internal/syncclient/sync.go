package syncclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncservice"
	"golang.org/x/sys/unix"
)

type CASError struct{ Expected, Actual string }

func (e *CASError) Error() string {
	return fmt.Sprintf("workspace changed: expected %s, got %s", e.Expected, e.Actual)
}

type BootstrapConflictError struct{ Local, Service []byte }

func (e *BootstrapConflictError) Error() string {
	return "local workspace differs from service workspace"
}

func (s *Store) Attach(ctx context.Context, root string, response syncservice.SyncResponse) error {
	path := filepath.Join(root, "workspace.toml")
	local, err := canonicalFile(root)
	if errors.Is(err, os.ErrNotExist) {
		if err = materialize(root, response.Workspace); err != nil {
			return err
		}
		return s.Initialize(ctx, response)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(local, response.Workspace) {
		return &BootstrapConflictError{Local: local, Service: append([]byte(nil), response.Workspace...)}
	}
	_ = path
	return s.Initialize(ctx, response)
}

func (c *Client) SyncWorkspace(ctx context.Context, store *Store, workspaceID, root string) (syncservice.SyncResponse, error) {
	lock, err := lockWorkspace(store.path + "." + workspaceID + ".lock")
	if err != nil {
		return syncservice.SyncResponse{}, err
	}
	defer unlockWorkspace(lock)
	state, err := store.State(ctx, workspaceID)
	if err != nil {
		return syncservice.SyncResponse{}, err
	}
	if state.Phase == "conflicted" {
		return syncservice.SyncResponse{State: state.Ref, Workspace: state.Observed, Conflicts: state.Conflicts}, nil
	}
	if state.Phase == "response-staged" {
		response, err := decodeStoredResponse(state.Response)
		if err != nil {
			return response, err
		}
		return response, recoverResponse(ctx, store, state, root, response)
	}
	if state.Phase == "clean" {
		prepared, response, done, err := c.prepareCleanSync(ctx, store, state, workspaceID, root)
		if done || err != nil {
			return response, err
		}
		state = prepared
	}
	return c.sendPendingSync(ctx, store, state, workspaceID, root)
}

func (c *Client) prepareCleanSync(ctx context.Context, store *Store, state WorkspaceState, workspaceID, root string) (WorkspaceState, syncservice.SyncResponse, bool, error) {
	desired, err := canonicalFile(root)
	if err != nil {
		return state, syncservice.SyncResponse{}, true, err
	}
	if !bytes.Equal(desired, state.Canonical) {
		request := syncservice.SyncRequest{RequestID: uuid.NewString(), WorkspaceID: workspaceID, ServiceID: state.Ref.ServiceID, ServiceEpoch: state.Ref.ServiceEpoch, BaseRevision: state.Ref.Revision, BaseSemanticHash: state.Ref.SemanticHash, Desired: desired}
		requestJSON, err := json.Marshal(request)
		if err != nil {
			return state, syncservice.SyncResponse{}, true, err
		}
		if err = store.stagePending(ctx, workspaceID, requestJSON, state.Canonical, desired, hash(desired)); err != nil {
			return state, syncservice.SyncResponse{}, true, err
		}
		state, err = store.State(ctx, workspaceID)
		return state, syncservice.SyncResponse{}, false, err
	}
	current, err := c.Current(ctx, workspaceID)
	if err != nil {
		return state, syncservice.SyncResponse{}, true, err
	}
	if err = verifyResponse(current, workspaceID, state.Ref.Revision); err != nil {
		return state, current, true, err
	}
	if current.State.SemanticHash == state.Ref.SemanticHash {
		return state, current, true, nil
	}
	return state, current, true, stageCurrentResponse(ctx, store, state, workspaceID, root, desired, current)
}

func stageCurrentResponse(ctx context.Context, store *Store, state WorkspaceState, workspaceID, root string, desired []byte, current syncservice.SyncResponse) error {
	if err := store.stagePending(ctx, workspaceID, nil, state.Canonical, state.Canonical, hash(desired)); err != nil {
		return err
	}
	exact, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err = store.stageResponse(ctx, workspaceID, exact); err != nil {
		return err
	}
	state.Response = exact
	state.Phase = "response-staged"
	state.ExpectedHash = hash(desired)
	return recoverResponse(ctx, store, state, root, current)
}

func (c *Client) sendPendingSync(ctx context.Context, store *Store, state WorkspaceState, workspaceID, root string) (syncservice.SyncResponse, error) {
	response, exact, err := c.sendSync(ctx, state.Request)
	if err != nil {
		return response, err
	}
	if err = c.verify(response.State.ServiceID, response.State.ServiceEpoch); err != nil {
		return response, err
	}
	if err = verifyResponse(response, workspaceID, state.Ref.Revision); err != nil {
		return response, err
	}
	if err = store.stageResponse(ctx, workspaceID, exact); err != nil {
		return response, err
	}
	state.Response = exact
	state.Phase = "response-staged"
	return response, recoverResponse(ctx, store, state, root, response)
}

func (c *Client) ResolveWorkspace(ctx context.Context, store *Store, workspaceID, root string) (syncservice.SyncResponse, error) {
	lock, err := lockWorkspace(store.path + "." + workspaceID + ".lock")
	if err != nil {
		return syncservice.SyncResponse{}, err
	}
	state, err := store.State(ctx, workspaceID)
	if err != nil {
		unlockWorkspace(lock)
		return syncservice.SyncResponse{}, err
	}
	if state.Phase != "conflicted" {
		unlockWorkspace(lock)
		return syncservice.SyncResponse{}, errors.New("workspace is not conflicted")
	}
	desired, err := canonicalFile(root)
	if err != nil {
		unlockWorkspace(lock)
		return syncservice.SyncResponse{}, err
	}
	request := syncservice.SyncRequest{RequestID: uuid.NewString(), WorkspaceID: workspaceID, ServiceID: state.Ref.ServiceID, ServiceEpoch: state.Ref.ServiceEpoch, BaseRevision: state.Ref.Revision, BaseSemanticHash: state.Ref.SemanticHash, Desired: desired}
	requestJSON, err := json.Marshal(request)
	if err == nil {
		err = store.resolveConflict(ctx, workspaceID, requestJSON, state.Observed, desired, hash(desired))
	}
	unlockWorkspace(lock)
	if err != nil {
		return syncservice.SyncResponse{}, err
	}
	return c.SyncWorkspace(ctx, store, workspaceID, root)
}

func recoverResponse(ctx context.Context, store *Store, state WorkspaceState, root string, response syncservice.SyncResponse) error {
	if err := verifyResponse(response, state.WorkspaceID, state.Ref.Revision); err != nil {
		return err
	}
	if len(response.Conflicts) != 0 {
		return store.recordConflict(ctx, state.WorkspaceID, response.State, response.Workspace, response.Conflicts)
	}
	current, err := canonicalFile(root)
	if err != nil {
		return err
	}
	actual := hash(current)
	if actual == response.State.SemanticHash {
		return store.finish(ctx, state.WorkspaceID, response)
	}
	if actual != state.ExpectedHash {
		return &CASError{Expected: state.ExpectedHash, Actual: actual}
	}
	if err = materialize(root, response.Workspace); err != nil {
		return err
	}
	return store.finish(ctx, state.WorkspaceID, response)
}

func verifyResponse(response syncservice.SyncResponse, workspaceID string, minimumRevision int64) error {
	if response.State.WorkspaceID != workspaceID {
		return errors.New("sync response workspace mismatch")
	}
	if response.State.Revision < minimumRevision {
		return errors.New("sync response revision regressed")
	}
	_, canonical, semanticHash, err := syncservice.Canonicalize(response.Workspace)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, response.Workspace) || semanticHash != response.State.SemanticHash {
		return errors.New("sync response semantic hash mismatch")
	}
	return nil
}

func canonicalFile(root string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, "workspace.toml"))
	if err != nil {
		return nil, err
	}
	_, canonical, _, err := syncservice.Canonicalize(b)
	return canonical, err
}

func materialize(root string, canonical []byte) error {
	workspace, err := config.DecodeCanonicalWorkspace(canonical)
	if err != nil {
		return err
	}
	workspace.RestoreRoot(root)
	return config.Save(root, workspace)
}

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func decodeSyncResponse(response *http.Response) (syncservice.SyncResponse, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return syncservice.SyncResponse{}, nil, err
	}
	if len(body) > maxResponseBytes {
		return syncservice.SyncResponse{}, nil, errors.New("response too large")
	}
	decoded, err := decodeStoredResponse(body)
	return decoded, body, err
}
func decodeStoredResponse(body []byte) (syncservice.SyncResponse, error) {
	var response syncservice.SyncResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return response, errors.New("invalid trailing response data")
	}
	return response, nil
}

func lockWorkspace(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func unlockWorkspace(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }
