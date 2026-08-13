package syncclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/syncservice"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

type WorkspaceState struct {
	WorkspaceID  string
	Phase        string
	Ref          syncservice.StateRef
	Canonical    []byte
	Request      []byte
	Response     []byte
	Base         []byte
	Desired      []byte
	Observed     []byte
	Conflicts    []syncservice.Conflict
	ExpectedHash string
}

func Open(path string) (*Store, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS workspace_state (
 workspace_id TEXT PRIMARY KEY, phase TEXT NOT NULL, state_ref BLOB NOT NULL, canonical BLOB NOT NULL,
 request BLOB, response BLOB, base BLOB, desired BLOB, observed BLOB, conflicts BLOB, expected_hash TEXT
);`); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, path: path}
	if err = store.restrict(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) restrict() error {
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Initialize(ctx context.Context, response syncservice.SyncResponse) error {
	ref, err := json.Marshal(response.State)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_state(workspace_id,phase,state_ref,canonical) VALUES(?,'clean',?,?) ON CONFLICT(workspace_id) DO UPDATE SET phase='clean',state_ref=excluded.state_ref,canonical=excluded.canonical,request=NULL,response=NULL,base=NULL,desired=NULL,observed=NULL,conflicts=NULL,expected_hash=NULL`, response.State.WorkspaceID, ref, response.Workspace)
	return err
}

func (s *Store) State(ctx context.Context, workspaceID string) (WorkspaceState, error) {
	var state WorkspaceState
	var ref, conflicts []byte
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id,phase,state_ref,canonical,request,response,base,desired,observed,conflicts,expected_hash FROM workspace_state WHERE workspace_id=?`, workspaceID).Scan(&state.WorkspaceID, &state.Phase, &ref, &state.Canonical, nullableBytes(&state.Request), nullableBytes(&state.Response), nullableBytes(&state.Base), nullableBytes(&state.Desired), nullableBytes(&state.Observed), nullableBytes(&conflicts), nullableString(&state.ExpectedHash))
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(ref, &state.Ref); err != nil {
		return state, err
	}
	if len(conflicts) != 0 {
		err = json.Unmarshal(conflicts, &state.Conflicts)
	}
	return state, err
}

type byteScanner struct{ target *[]byte }

func nullableBytes(target *[]byte) *byteScanner { return &byteScanner{target} }
func (s *byteScanner) Scan(value any) error {
	if value == nil {
		*s.target = nil
		return nil
	}
	*s.target = append((*s.target)[:0], value.([]byte)...)
	return nil
}

type stringScanner struct{ target *string }

func nullableString(target *string) *stringScanner { return &stringScanner{target} }
func (s *stringScanner) Scan(value any) error {
	if value == nil {
		*s.target = ""
		return nil
	}
	*s.target = value.(string)
	return nil
}

func (s *Store) stagePending(ctx context.Context, workspaceID string, request, base, desired []byte, expected string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_state SET phase='pending',request=?,response=NULL,base=?,desired=?,observed=NULL,conflicts=NULL,expected_hash=? WHERE workspace_id=? AND phase='clean'`, request, base, desired, expected, workspaceID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return errors.New("workspace is not clean")
	}
	return nil
}

func (s *Store) stageResponse(ctx context.Context, workspaceID string, response []byte) error {
	return updateOne(s.db.ExecContext(ctx, `UPDATE workspace_state SET phase='response-staged',response=? WHERE workspace_id=? AND phase='pending'`, response, workspaceID))
}

func (s *Store) finish(ctx context.Context, workspaceID string, response syncservice.SyncResponse) error {
	ref, _ := json.Marshal(response.State)
	return updateOne(s.db.ExecContext(ctx, `UPDATE workspace_state SET phase='clean',state_ref=?,canonical=?,request=NULL,response=NULL,base=NULL,desired=NULL,observed=NULL,conflicts=NULL,expected_hash=NULL WHERE workspace_id=?`, ref, response.Workspace, workspaceID))
}

func (s *Store) recordConflict(ctx context.Context, workspaceID string, ref syncservice.StateRef, observed []byte, conflicts []syncservice.Conflict) error {
	body, _ := json.Marshal(conflicts)
	stateRef, _ := json.Marshal(ref)
	return updateOne(s.db.ExecContext(ctx, `UPDATE workspace_state SET phase='conflicted',state_ref=?,observed=?,conflicts=?,response=NULL,request=NULL WHERE workspace_id=?`, stateRef, observed, body, workspaceID))
}

func (s *Store) resolveConflict(ctx context.Context, workspaceID string, request, base, desired []byte, expected string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE workspace_state SET phase='pending',request=?,response=NULL,base=?,desired=?,observed=NULL,conflicts=NULL,expected_hash=? WHERE workspace_id=? AND phase='conflicted'`, request, base, desired, expected, workspaceID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return errors.New("workspace is not conflicted")
	}
	return nil
}

func updateOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("workspace phase changed unexpectedly")
	}
	return nil
}
