package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
	_ "modernc.org/sqlite"
)

var (
	ErrWorkspaceNotFound   = errors.New("workspace not found")
	ErrWorkspaceEpochStale = errors.New("workspace epoch is stale")
	ErrStaleRevision       = errors.New("workspace revision changed")
)

type Workspace struct {
	Name        string
	Root        string
	Revision    int64
	WorkspaceID string
	Epoch       int64
	Head        string
	State       *config.Workspace
}

type Store struct {
	db       *sql.DB
	path     string
	identity device.Identity
}

func Open(path string) (*Store, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	identity, err := loadRegistryIdentity(database, filepath.Join(directory, "identity.key"))
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if _, err = database.Exec(`PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;`); err != nil {
		_ = database.Close()
		return nil, err
	}
	store := &Store{db: database, path: path, identity: identity}
	if err = store.initialize(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err = store.restrict(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func loadRegistryIdentity(database *sql.DB, path string) (device.Identity, error) {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return device.Identity{}, err
		}
		return device.Load(path)
	}
	initialized, err := registryHasIdentityBoundState(database)
	if err != nil {
		return device.Identity{}, err
	}
	if initialized {
		return device.Identity{}, errors.New("device identity is missing for initialized registry")
	}
	return device.Load(path)
}

func registryHasIdentityBoundState(database *sql.DB) (bool, error) {
	for _, table := range []string{"network_events", "revision_proofs", "workspace_protocol"} {
		var exists int
		if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return false, err
		}
		if exists == 0 {
			continue
		}
		if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM ` + table + ` LIMIT 1)`).Scan(&exists); err != nil {
			return false, err
		}
		if exists != 0 {
			return true, nil
		}
	}
	return false, nil
}

func OpenDefault() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func Exists() (bool, error) {
	path, err := DefaultPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) Create(ctx context.Context, name, root string, state *config.Workspace) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return Workspace{}, err
	}
	snapshotBody, err := encodeSnapshot(state)
	if err != nil {
		return Workspace{}, err
	}
	workspaceID := newWorkspaceID()
	genesis, err := makeRevision(workspaceID, 1, "genesis", nil, snapshotBody, nil, localPolicy(store.identity.ID()), store.identity)
	if err != nil {
		return Workspace{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,1,?)`, name, canonical, body); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	if err = insertRevision(tx, genesis); err != nil {
		return Workspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,1,?)`, name, workspaceID, genesis.ID); err != nil {
		return Workspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, workspaceID, genesis.ID); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return Workspace{}, err
	}
	state.RestoreRoot(canonical)
	return Workspace{Name: name, Root: canonical, Revision: 1, WorkspaceID: workspaceID, Epoch: 1, Head: genesis.ID, State: state}, nil
}

func (store *Store) LoadByName(ctx context.Context, name string) (Workspace, error) {
	return loadWorkspaceByName(ctx, store.db, name)
}

func loadWorkspaceByName(ctx context.Context, reader sqlReader, name string) (Workspace, error) {
	return scanWorkspace(reader.QueryRowContext(ctx, `SELECT w.name,w.root,w.revision,p.workspace_id,p.epoch,p.head_id,w.registry FROM workspaces w JOIN workspace_protocol p ON p.name=w.name WHERE w.name=?`, name))
}

func (store *Store) LoadByRoot(ctx context.Context, root string) (Workspace, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	workspace, err := scanWorkspace(store.db.QueryRowContext(ctx, `SELECT w.name,w.root,w.revision,p.workspace_id,p.epoch,p.head_id,w.registry FROM workspaces w JOIN workspace_protocol p ON p.name=w.name WHERE w.root=?`, canonical))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, fmt.Errorf("%w for root %q", ErrWorkspaceNotFound, canonical)
	}
	return workspace, err
}

func (store *Store) SetRoot(ctx context.Context, name, root string) (Workspace, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE workspaces SET root=? WHERE name=?`, canonical, name)
	if err != nil {
		return Workspace{}, fmt.Errorf("set workspace root: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Workspace{}, err
	}
	if affected != 1 {
		return Workspace{}, fmt.Errorf("%w: %q", ErrWorkspaceNotFound, name)
	}
	return store.LoadByName(ctx, name)
}

func (store *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT w.name,w.root,w.revision,p.workspace_id,p.epoch,p.head_id,w.registry FROM workspaces w JOIN workspace_protocol p ON p.name=w.name ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var workspaces []Workspace
	for rows.Next() {
		workspace, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

func (store *Store) Find(ctx context.Context, path string) (Workspace, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	workspaces, err := store.List(ctx)
	if err != nil {
		return Workspace{}, err
	}
	var found Workspace
	for _, candidate := range workspaces {
		relative, relErr := filepath.Rel(candidate.Root, absolute)
		outside := relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
		if relErr == nil && !outside && !filepath.IsAbs(relative) && (found.Root == "" || len(candidate.Root) > len(found.Root)) {
			found = candidate
		}
	}
	if found.Root == "" {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return found, nil
}

func (store *Store) Update(ctx context.Context, name string, expectedRevision int64, state *config.Workspace) (Workspace, error) {
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return Workspace{}, err
	}
	snapshotBody, err := encodeSnapshot(state)
	if err != nil {
		return Workspace{}, err
	}
	localActive, networkPresent, err := store.localNetworkPresence(ctx)
	if err != nil {
		return Workspace{}, err
	}
	if err = store.persistUpdate(ctx, name, expectedRevision, body, snapshotBody, localActive, networkPresent); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, name)
}

func (store *Store) persistUpdate(ctx context.Context, name string, expectedRevision int64, body, snapshotBody []byte, localActive, networkPresent bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	workspaceID, epoch, head, err := updateBase(ctx, tx, name, expectedRevision)
	if err != nil {
		return err
	}
	if err = requireNoAccessConflict(tx, workspaceID); err != nil {
		return err
	}
	policy, err := policyAtTx(tx, head)
	if err != nil {
		return err
	}
	if err = requireLocalWriter(policy, store.identity.ID(), localActive, networkPresent); err != nil {
		return err
	}
	if err = validateSharedUpdate(policy, store.identity.ID(), snapshotBody); err != nil {
		return err
	}
	conflicts, err := loadRevisionConflicts(tx, head)
	if err != nil {
		return err
	}
	revision, err := makeRevision(workspaceID, epoch, "ordinary", []string{head}, snapshotBody, conflicts, policy, store.identity)
	if err != nil {
		return err
	}
	if err = insertRevision(tx, revision); err != nil {
		return err
	}
	if err = persistWorkspaceUpdate(ctx, tx, name, expectedRevision, body, head, revision.ID); err != nil {
		return err
	}
	if err = replaceHeads(ctx, tx, workspaceID, []string{revision.ID}); err != nil {
		return err
	}
	if err = replaceConflicts(ctx, tx, workspaceID, revision.ID, conflicts); err != nil {
		return err
	}
	return tx.Commit()
}

func validateSharedUpdate(policy AccessPolicy, localID string, snapshot []byte) error {
	if !policySharedWithOtherDevice(policy, localID) {
		return nil
	}
	return validateShareableSnapshot(snapshot)
}

func persistWorkspaceUpdate(ctx context.Context, tx *sql.Tx, name string, expectedRevision int64, body []byte, head, revisionID string) error {
	result, err := tx.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, expectedRevision)
	if err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleRevision
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET head_id=? WHERE name=? AND head_id=?`, revisionID, name, head)
	return err
}

func (store *Store) localNetworkPresence(ctx context.Context) (bool, bool, error) {
	network, err := store.Network(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	for _, record := range network.Devices {
		if record.ID == store.identity.ID() {
			return record.Active, true, nil
		}
	}
	return false, true, nil
}

func requireLocalWriter(policy AccessPolicy, deviceID string, active, networkPresent bool) error {
	role := policy.Role(deviceID, active)
	if !networkPresent && role == "" {
		role = WorkspaceWriter
	}
	if role != WorkspaceAdmin && role != WorkspaceWriter {
		return errors.New("local device cannot write this workspace")
	}
	return nil
}

func updateBase(ctx context.Context, tx *sql.Tx, name string, expectedRevision int64) (string, int64, string, error) {
	var workspaceID, head string
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT p.workspace_id,p.epoch,p.head_id FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=? AND w.revision=?`, name, expectedRevision).Scan(&workspaceID, &epoch, &head)
	if !errors.Is(err, sql.ErrNoRows) {
		return workspaceID, epoch, head, err
	}
	exists, err := workspaceExists(tx, name)
	if err != nil {
		return "", 0, "", err
	}
	if !exists {
		return "", 0, "", fmt.Errorf("%w: %q", ErrWorkspaceNotFound, name)
	}
	return "", 0, "", ErrStaleRevision
}

func (store *Store) Mutate(ctx context.Context, root string, mutate func(*config.Workspace) error) (Workspace, error) {
	workspace, err := store.LoadByRoot(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	if err = mutate(workspace.State); err != nil {
		return Workspace{}, err
	}
	return store.Update(ctx, workspace.Name, workspace.Revision, workspace.State)
}

type scanner interface {
	Scan(...any) error
}

type sqlReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanWorkspace(row scanner) (Workspace, error) {
	var workspace Workspace
	var body []byte
	if err := row.Scan(&workspace.Name, &workspace.Root, &workspace.Revision, &workspace.WorkspaceID, &workspace.Epoch, &workspace.Head, &body); err != nil {
		return workspace, err
	}
	state, err := config.DecodeStoredWorkspace(body)
	if err != nil {
		return workspace, err
	}
	state.RestoreRoot(workspace.Root)
	workspace.State = state
	return workspace, nil
}

func (store *Store) restrict() error {
	for _, candidate := range []string{store.path, store.path + "-wal", store.path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace root must be a directory")
	}
	return filepath.Clean(resolved), nil
}
