package syncservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kuchmenko/workspace/internal/config"
	_ "modernc.org/sqlite"
)

var ErrSingleWorkspaceAlreadyImported = errors.New("single-workspace store already has an imported workspace")
var ErrIdempotencyMismatch = errors.New("request ID payload mismatch")
var ErrStateMismatch = errors.New("state reference mismatch")
var ErrBaseNotFound = errors.New("base revision not found")
var ErrPairingInvalid = errors.New("pairing token is invalid, expired, or consumed")
var ErrPairingMismatch = errors.New("pairing retry does not match the original attempt")
var ErrClientUnauthorized = errors.New("client is inactive or unknown")

type Store struct {
	db        *sql.DB
	serviceID string
	epoch     string
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
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err = createSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db}
	if err = store.loadMetadata(); err != nil {
		db.Close()
		return nil, err
	}
	if err = restrictDatabaseFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func restrictDatabaseFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Backup(stateDir string) (string, error) {
	directory := filepath.Join(stateDir, "backups")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "service-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".db")
	quoted := strings.ReplaceAll(path, "'", "''")
	if _, err := s.db.Exec(`VACUUM INTO '` + quoted + `'`); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) ServiceRef() StateRef { return StateRef{ServiceID: s.serviceID, ServiceEpoch: s.epoch} }

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS service (singleton INTEGER PRIMARY KEY CHECK(singleton=1), service_id TEXT NOT NULL, epoch TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS workspaces (workspace_id TEXT PRIMARY KEY, current_revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS revisions (workspace_id TEXT NOT NULL, revision INTEGER NOT NULL, semantic_hash TEXT NOT NULL, canonical BLOB NOT NULL, PRIMARY KEY(workspace_id,revision), FOREIGN KEY(workspace_id) REFERENCES workspaces(workspace_id));
CREATE TABLE IF NOT EXISTS requests (workspace_id TEXT NOT NULL, actor_id TEXT NOT NULL, request_id TEXT NOT NULL, payload_hash TEXT NOT NULL, response BLOB NOT NULL, PRIMARY KEY(workspace_id,actor_id,request_id), FOREIGN KEY(workspace_id) REFERENCES workspaces(workspace_id));`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS clients (actor_id TEXT PRIMARY KEY, display_name TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('client','admin')), certificate_serial TEXT NOT NULL UNIQUE, public_key_hash TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS pairing_tokens (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, role TEXT NOT NULL CHECK(role IN ('client','admin')), attempt_id TEXT, csr_hash TEXT, response BLOB);`)
	return err
}

func (s *Store) CreatePairing(role Role, ttl time.Duration) (Pairing, error) {
	if (role != RoleClient && role != RoleAdmin) || ttl <= 0 {
		return Pairing{}, fmt.Errorf("valid role and positive TTL are required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Pairing{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(`INSERT INTO pairing_tokens(token_hash,expires_at,role) VALUES(?,?,?)`, hashString(token), expires.Unix(), role)
	return Pairing{Token: token, Role: role, ExpiresAt: expires}, err
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}

func (s *Store) Pair(ctx context.Context, request PairRequest, identity *Identity) (PairResponse, error) {
	if request.Token == "" || request.AttemptID == "" || request.DisplayName == "" || request.CSR == "" {
		return PairResponse{}, ErrPairingInvalid
	}
	csrHash := hashString(request.CSR)
	var response PairResponse
	err := withTx(ctx, s.db, func(tx *sql.Tx) error {
		var expires int64
		var role Role
		var attemptID, storedCSR sql.NullString
		var storedResponse []byte
		err := tx.QueryRow(`SELECT expires_at,role,attempt_id,csr_hash,response FROM pairing_tokens WHERE token_hash=?`, hashString(request.Token)).Scan(&expires, &role, &attemptID, &storedCSR, &storedResponse)
		if errors.Is(err, sql.ErrNoRows) || expires < time.Now().Unix() {
			return ErrPairingInvalid
		}
		if err != nil {
			return err
		}
		if attemptID.Valid {
			if attemptID.String != request.AttemptID || storedCSR.String != csrHash {
				return ErrPairingMismatch
			}
			return json.Unmarshal(storedResponse, &response)
		}
		actorID := uuid.NewString()
		issued, serial, publicKeyHash, err := identity.issueClient(request.CSR, actorID)
		if err != nil {
			return err
		}
		response = PairResponse{ActorID: actorID, Role: role, CAPEM: string(identity.CAPEM()), Certificate: string(issued), ServiceID: s.serviceID, ServiceEpoch: s.epoch}
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO clients(actor_id,display_name,role,certificate_serial,public_key_hash,active) VALUES(?,?,?,?,?,1)`, actorID, request.DisplayName, role, serial, publicKeyHash); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE pairing_tokens SET attempt_id=?,csr_hash=?,response=? WHERE token_hash=? AND attempt_id IS NULL`, request.AttemptID, csrHash, body, hashString(request.Token))
		return err
	})
	return response, err
}

func (s *Store) AuthenticateClient(ctx context.Context, serial, publicKeyHash string) (Client, error) {
	var client Client
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT actor_id,display_name,role,certificate_serial,public_key_hash,active FROM clients WHERE certificate_serial=? AND public_key_hash=?`, serial, publicKeyHash).Scan(&client.ActorID, &client.DisplayName, &client.Role, &client.Serial, &client.PublicKeyHash, &active)
	client.Active = active == 1
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !client.Active) {
		return Client{}, ErrClientUnauthorized
	}
	return client, err
}

func (s *Store) RevokeClient(ctx context.Context, actorID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE clients SET active=0 WHERE actor_id=? AND active=1`, actorID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrClientUnauthorized
	}
	return nil
}

func (s *Store) loadMetadata() error {
	return withTx(context.Background(), s.db, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT service_id, epoch FROM service WHERE singleton=1`).Scan(&s.serviceID, &s.epoch)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		s.serviceID, s.epoch = uuid.NewString(), uuid.NewString()
		_, err = tx.Exec(`INSERT INTO service(singleton,service_id,epoch) VALUES(1,?,?)`, s.serviceID, s.epoch)
		return err
	})
}

func (s *Store) AdminImportSingleWorkspace(ctx context.Context, workspace []byte) (StateRef, error) {
	_, canonical, hash, err := Canonicalize(workspace)
	if err != nil {
		return StateRef{}, err
	}
	ref := StateRef{ServiceID: s.serviceID, ServiceEpoch: s.epoch, WorkspaceID: uuid.NewString(), Revision: 1, SemanticHash: hash}
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		var workspaceID, existingHash string
		var revision int64
		err := tx.QueryRow(`SELECT w.workspace_id,r.revision,r.semantic_hash FROM workspaces w JOIN revisions r ON r.workspace_id=w.workspace_id AND r.revision=1 LIMIT 1`).Scan(&workspaceID, &revision, &existingHash)
		if err == nil {
			if existingHash == hash {
				ref.WorkspaceID, ref.Revision, ref.SemanticHash = workspaceID, revision, existingHash
				return nil
			}
			return ErrSingleWorkspaceAlreadyImported
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO workspaces(workspace_id,current_revision) VALUES(?,1)`, ref.WorkspaceID); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO revisions(workspace_id,revision,semantic_hash,canonical) VALUES(?,1,?,?)`, ref.WorkspaceID, hash, canonical)
		return err
	})
	return ref, err
}

func (s *Store) Current(ctx context.Context, workspaceID string) (SyncResponse, error) {
	return s.read(ctx, workspaceID, 0)
}

func (s *Store) Revision(ctx context.Context, workspaceID string, revision int64) (SyncResponse, error) {
	return s.read(ctx, workspaceID, revision)
}

func (s *Store) read(ctx context.Context, workspaceID string, revision int64) (SyncResponse, error) {
	query := `SELECT r.revision,r.semantic_hash,r.canonical FROM revisions r JOIN workspaces w ON w.workspace_id=r.workspace_id WHERE r.workspace_id=? AND r.revision=w.current_revision`
	args := []any{workspaceID}
	if revision != 0 {
		query = `SELECT revision,semantic_hash,canonical FROM revisions WHERE workspace_id=? AND revision=?`
		args = append(args, revision)
	}
	var response SyncResponse
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&response.State.Revision, &response.State.SemanticHash, &response.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return response, ErrBaseNotFound
	}
	response.State.ServiceID, response.State.ServiceEpoch, response.State.WorkspaceID = s.serviceID, s.epoch, workspaceID
	return response, err
}

func (s *Store) Sync(ctx context.Context, actorID string, request SyncRequest) (SyncResponse, error) {
	if actorID == "" || request.RequestID == "" {
		return SyncResponse{}, fmt.Errorf("actor and request ID are required")
	}
	if request.ServiceID != s.serviceID || request.ServiceEpoch != s.epoch {
		return SyncResponse{}, ErrStateMismatch
	}
	desired, desiredCanonical, _, err := Canonicalize(request.Desired)
	if err != nil {
		return SyncResponse{}, err
	}
	payloadHash := semanticHash(append([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00", request.WorkspaceID, request.BaseRevision, request.BaseSemanticHash, request.ServiceID, request.ServiceEpoch)), desiredCanonical...))
	var response SyncResponse
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		return s.syncTx(tx, actorID, request, desired, payloadHash, &response)
	})
	return response, err
}

func (s *Store) syncTx(tx *sql.Tx, actorID string, request SyncRequest, desired *config.Workspace, payloadHash string, response *SyncResponse) error {
	var storedHash string
	var body []byte
	err := tx.QueryRow(`SELECT payload_hash,response FROM requests WHERE workspace_id=? AND actor_id=? AND request_id=?`, request.WorkspaceID, actorID, request.RequestID).Scan(&storedHash, &body)
	if err == nil {
		if storedHash != payloadHash {
			return ErrIdempotencyMismatch
		}
		return json.Unmarshal(body, response)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = s.mergeSyncRequest(tx, request, desired, response); err != nil {
		return err
	}
	body, err = json.Marshal(*response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO requests(workspace_id,actor_id,request_id,payload_hash,response) VALUES(?,?,?,?,?)`, request.WorkspaceID, actorID, request.RequestID, payloadHash, body)
	return err
}

func (s *Store) mergeSyncRequest(tx *sql.Tx, request SyncRequest, desired *config.Workspace, response *SyncResponse) error {
	baseResponse, err := readTx(tx, s, request.WorkspaceID, request.BaseRevision)
	if err != nil {
		return err
	}
	if baseResponse.State.SemanticHash != request.BaseSemanticHash {
		return ErrStateMismatch
	}
	current, err := readTx(tx, s, request.WorkspaceID, 0)
	if err != nil {
		return err
	}
	base, err := config.DecodeCanonicalWorkspace(baseResponse.Workspace)
	if err != nil {
		return err
	}
	service, err := config.DecodeCanonicalWorkspace(current.Workspace)
	if err != nil {
		return err
	}
	merged, conflicts, err := Merge(base, desired, service)
	if err != nil {
		return err
	}
	*response = current
	if len(conflicts) != 0 {
		response.Conflicts = conflicts
		return nil
	}
	return storeMergedRevision(tx, request.WorkspaceID, merged, response)
}

func storeMergedRevision(tx *sql.Tx, workspaceID string, merged *config.Workspace, response *SyncResponse) error {
	canonical, err := config.EncodeCanonicalWorkspace(merged)
	if err != nil {
		return err
	}
	hash := semanticHash(canonical)
	if hash == response.State.SemanticHash {
		return nil
	}
	response.State.Revision++
	response.State.SemanticHash = hash
	response.Workspace = canonical
	if _, err = tx.Exec(`INSERT INTO revisions(workspace_id,revision,semantic_hash,canonical) VALUES(?,?,?,?)`, workspaceID, response.State.Revision, hash, canonical); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE workspaces SET current_revision=? WHERE workspace_id=?`, response.State.Revision, workspaceID)
	return err
}

func readTx(tx *sql.Tx, s *Store, workspaceID string, revision int64) (SyncResponse, error) {
	query := `SELECT r.revision,r.semantic_hash,r.canonical FROM revisions r JOIN workspaces w ON w.workspace_id=r.workspace_id WHERE r.workspace_id=? AND r.revision=w.current_revision`
	args := []any{workspaceID}
	if revision != 0 {
		query = `SELECT revision,semantic_hash,canonical FROM revisions WHERE workspace_id=? AND revision=?`
		args = append(args, revision)
	}
	var out SyncResponse
	err := tx.QueryRow(query, args...).Scan(&out.State.Revision, &out.State.SemanticHash, &out.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrBaseNotFound
	}
	out.State = StateRef{ServiceID: s.serviceID, ServiceEpoch: s.epoch, WorkspaceID: workspaceID, Revision: out.State.Revision, SemanticHash: out.State.SemanticHash}
	return out, err
}

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
