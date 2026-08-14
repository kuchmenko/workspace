package syncnode

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncprotocol"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrStaleHead         = errors.New("workspace head changed")
	ErrMultipleHeads     = errors.New("workspace has multiple heads")
)

type Workspace struct {
	ID    syncprotocol.WorkspaceID
	Epoch syncprotocol.RecoveryEpoch
	Name  string
	Root  string
	Head  syncprotocol.RevisionID
	State *config.Workspace
}

func OpenStore(path string) (*Store, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err = database.Exec(`PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS workspaces (
 workspace_id BLOB PRIMARY KEY,
 recovery_epoch BLOB NOT NULL,
 name TEXT NOT NULL UNIQUE,
 root TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS revisions (
 revision_id BLOB PRIMARY KEY,
 workspace_id BLOB NOT NULL,
 recovery_epoch BLOB NOT NULL,
 generation INTEGER NOT NULL,
 kind INTEGER NOT NULL,
 author BLOB,
 author_sequence INTEGER,
 core BLOB NOT NULL,
 FOREIGN KEY(workspace_id) REFERENCES workspaces(workspace_id)
);
CREATE TABLE IF NOT EXISTS revision_parents (
 revision_id BLOB NOT NULL,
 position INTEGER NOT NULL,
 parent_id BLOB NOT NULL,
 PRIMARY KEY(revision_id, position),
 UNIQUE(revision_id, parent_id),
 FOREIGN KEY(revision_id) REFERENCES revisions(revision_id)
);
CREATE TABLE IF NOT EXISTS workspace_heads (
 workspace_id BLOB NOT NULL,
 revision_id BLOB NOT NULL,
 PRIMARY KEY(workspace_id, revision_id),
 FOREIGN KEY(workspace_id) REFERENCES workspaces(workspace_id),
 FOREIGN KEY(revision_id) REFERENCES revisions(revision_id)
);
CREATE TABLE IF NOT EXISTS revision_proofs (
 revision_id BLOB NOT NULL,
 node_id BLOB NOT NULL,
 signature BLOB NOT NULL,
 PRIMARY KEY(revision_id, node_id),
 FOREIGN KEY(revision_id) REFERENCES revisions(revision_id)
);`); err != nil {
		database.Close()
		return nil, err
	}
	store := &Store{db: database, path: path}
	if err = store.restrict(); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) Import(ctx context.Context, name, root string, workspace *config.Workspace, identity Identity, recoveryPublicKey []byte) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	canonicalRoot, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	member := syncprotocol.Member{NodeID: identity.NodeID(), PublicKey: identity.PublicKey(), Role: syncprotocol.RoleAdmin}
	snapshot, err := syncprotocol.NewWorkspaceSnapshot(workspace, []syncprotocol.Member{member}, 1, recoveryPublicKey)
	if err != nil {
		return Workspace{}, err
	}
	snapshotBytes, err := syncprotocol.EncodeWorkspaceSnapshot(snapshot)
	if err != nil {
		return Workspace{}, err
	}
	var workspaceID syncprotocol.WorkspaceID
	var epoch syncprotocol.RecoveryEpoch
	if _, err = rand.Read(workspaceID[:]); err != nil {
		return Workspace{}, err
	}
	if _, err = rand.Read(epoch[:]); err != nil {
		return Workspace{}, err
	}
	core := syncprotocol.RevisionCore{
		ProtocolVersion: syncprotocol.ProtocolVersion,
		WorkspaceID:     workspaceID,
		RecoveryEpoch:   epoch,
		Kind:            syncprotocol.RevisionGenesis,
		Parents:         []syncprotocol.RevisionID{},
		SnapshotSchema:  syncprotocol.SnapshotSchemaVersion,
		Snapshot:        snapshotBytes,
		Conflicts:       []byte{0x80},
	}
	revisionID, err := syncprotocol.RevisionIDFor(core)
	if err != nil {
		return Workspace{}, err
	}
	proof, err := identity.Sign(revisionID)
	if err != nil {
		return Workspace{}, err
	}
	if !syncprotocol.VerifyRevisionProof(identity.PublicKey(), revisionID, proof) {
		return Workspace{}, errors.New("generated genesis proof is invalid")
	}
	coreBytes, err := syncprotocol.EncodeRevisionCore(core)
	if err != nil {
		return Workspace{}, err
	}
	err = withTx(ctx, store.db, func(transaction *sql.Tx) error {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO workspaces(workspace_id,recovery_epoch,name,root) VALUES(?,?,?,?)`, workspaceID[:], epoch[:], name, canonicalRoot); err != nil {
			return err
		}
		if _, err = transaction.ExecContext(ctx, `INSERT INTO revisions(revision_id,workspace_id,recovery_epoch,generation,kind,core) VALUES(?,?,?,?,?,?)`, revisionID[:], workspaceID[:], epoch[:], 0, core.Kind, coreBytes); err != nil {
			return err
		}
		if _, err = transaction.ExecContext(ctx, `INSERT INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, workspaceID[:], revisionID[:]); err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `INSERT INTO revision_proofs(revision_id,node_id,signature) VALUES(?,?,?)`, revisionID[:], proof.NodeID[:], proof.Signature)
		return err
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("import workspace: %w", err)
	}
	return Workspace{ID: workspaceID, Epoch: epoch, Name: name, Root: canonicalRoot, Head: revisionID, State: snapshot.Workspace(canonicalRoot)}, nil
}

func (store *Store) LoadByName(ctx context.Context, name string) (Workspace, error) {
	return store.load(ctx, `SELECT w.workspace_id,w.recovery_epoch,w.name,w.root,h.revision_id,r.core FROM workspaces w JOIN workspace_heads h ON h.workspace_id=w.workspace_id JOIN revisions r ON r.revision_id=h.revision_id WHERE w.name=?`, name)
}

func (store *Store) LoadByRoot(ctx context.Context, root string) (Workspace, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	workspace, err := store.load(ctx, `SELECT w.workspace_id,w.recovery_epoch,w.name,w.root,h.revision_id,r.core FROM workspaces w JOIN workspace_heads h ON h.workspace_id=w.workspace_id JOIN revisions r ON r.revision_id=h.revision_id WHERE w.root=?`, canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, fmt.Errorf("%w for root %q", ErrWorkspaceNotFound, canonical)
	}
	return workspace, err
}

func (store *Store) Commit(ctx context.Context, name string, expectedHead syncprotocol.RevisionID, state *config.Workspace, identity Identity) (Workspace, error) {
	if state == nil {
		return Workspace{}, errors.New("workspace state is required")
	}
	var committed Workspace
	err := withTx(ctx, store.db, func(transaction *sql.Tx) error {
		var parentCore syncprotocol.RevisionCore
		var parentSnapshot syncprotocol.WorkspaceSnapshot
		var err error
		committed, parentCore, parentSnapshot, err = loadCommitBase(ctx, transaction, name, expectedHead)
		if err != nil {
			return err
		}
		revision, err := buildRevision(ctx, transaction, committed, parentCore, parentSnapshot, state, identity)
		if err != nil {
			return err
		}
		if err = persistRevision(ctx, transaction, committed, expectedHead, revision); err != nil {
			return err
		}
		committed.Head = revision.id
		committed.State = revision.snapshot.Workspace(committed.Root)
		return nil
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("commit workspace: %w", err)
	}
	return committed, nil
}

type builtRevision struct {
	core     syncprotocol.RevisionCore
	id       syncprotocol.RevisionID
	proof    syncprotocol.SignatureProof
	snapshot syncprotocol.WorkspaceSnapshot
}

func loadCommitBase(ctx context.Context, transaction *sql.Tx, name string, expectedHead syncprotocol.RevisionID) (Workspace, syncprotocol.RevisionCore, syncprotocol.WorkspaceSnapshot, error) {
	var workspace Workspace
	var workspaceID, epoch, head, parentCoreBytes []byte
	err := transaction.QueryRowContext(ctx, `SELECT w.workspace_id,w.recovery_epoch,w.name,w.root,h.revision_id,r.core FROM workspaces w JOIN workspace_heads h ON h.workspace_id=w.workspace_id JOIN revisions r ON r.revision_id=h.revision_id WHERE w.name=?`, name).Scan(&workspaceID, &epoch, &workspace.Name, &workspace.Root, &head, &parentCoreBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, fmt.Errorf("%w: %q", ErrWorkspaceNotFound, name)
	}
	if err != nil {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, err
	}
	if len(workspaceID) != len(workspace.ID) || len(epoch) != len(workspace.Epoch) || len(head) != len(workspace.Head) {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, errors.New("stored workspace identity has invalid length")
	}
	copy(workspace.ID[:], workspaceID)
	copy(workspace.Epoch[:], epoch)
	copy(workspace.Head[:], head)
	if workspace.Head != expectedHead {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, ErrStaleHead
	}
	var headCount int
	if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_heads WHERE workspace_id=?`, workspace.ID[:]).Scan(&headCount); err != nil {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, err
	}
	if headCount != 1 {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, ErrMultipleHeads
	}
	parentCore, err := syncprotocol.DecodeRevisionCore(parentCoreBytes)
	if err != nil {
		return workspace, syncprotocol.RevisionCore{}, syncprotocol.WorkspaceSnapshot{}, err
	}
	parentSnapshot, err := syncprotocol.DecodeWorkspaceSnapshot(parentCore.Snapshot)
	return workspace, parentCore, parentSnapshot, err
}

func buildRevision(ctx context.Context, transaction *sql.Tx, workspace Workspace, parentCore syncprotocol.RevisionCore, parentSnapshot syncprotocol.WorkspaceSnapshot, state *config.Workspace, identity Identity) (builtRevision, error) {
	role, authorized := memberRole(parentSnapshot, identity.NodeID())
	if !authorized || role == syncprotocol.RoleReplica {
		return builtRevision{}, errors.New("local node is not authorized to write this workspace")
	}
	nextSnapshot, err := syncprotocol.NewWorkspaceSnapshot(state, parentSnapshot.Members, parentSnapshot.AdminThreshold, ed25519.PublicKey(parentSnapshot.RecoveryPublicKey))
	if err != nil {
		return builtRevision{}, err
	}
	nextSnapshotBytes, err := syncprotocol.EncodeWorkspaceSnapshot(nextSnapshot)
	if err != nil {
		return builtRevision{}, err
	}
	kind, err := revisionKind(parentSnapshot, nextSnapshot, role)
	if err != nil {
		return builtRevision{}, err
	}
	core := syncprotocol.RevisionCore{
		ProtocolVersion: syncprotocol.ProtocolVersion,
		WorkspaceID:     workspace.ID,
		RecoveryEpoch:   workspace.Epoch,
		Kind:            kind,
		Parents:         []syncprotocol.RevisionID{workspace.Head},
		Generation:      parentCore.Generation + 1,
		SnapshotSchema:  syncprotocol.SnapshotSchemaVersion,
		Snapshot:        nextSnapshotBytes,
		Conflicts:       parentCore.Conflicts,
	}
	if kind == syncprotocol.RevisionWrite {
		if err = setAuthor(ctx, transaction, &core, identity.NodeID()); err != nil {
			return builtRevision{}, err
		}
	}
	revisionID, err := syncprotocol.RevisionIDFor(core)
	if err != nil {
		return builtRevision{}, err
	}
	proof, err := identity.Sign(revisionID)
	if err != nil {
		return builtRevision{}, err
	}
	if !syncprotocol.VerifyRevisionProof(identity.PublicKey(), revisionID, proof) {
		return builtRevision{}, errors.New("generated revision proof is invalid")
	}
	return builtRevision{core: core, id: revisionID, proof: proof, snapshot: nextSnapshot}, nil
}

func revisionKind(previous, next syncprotocol.WorkspaceSnapshot, role syncprotocol.Role) (syncprotocol.RevisionKind, error) {
	if !requiresAdmin(previous.Registry, next.Registry) {
		return syncprotocol.RevisionWrite, nil
	}
	if role != syncprotocol.RoleAdmin {
		return 0, errors.New("workspace change requires an administrator")
	}
	if previous.AdminThreshold != 1 {
		return 0, errors.New("workspace change requires additional administrator proofs")
	}
	return syncprotocol.RevisionAuthority, nil
}

func setAuthor(ctx context.Context, transaction *sql.Tx, core *syncprotocol.RevisionCore, nodeID syncprotocol.NodeID) error {
	core.Author = nodeID
	var previousRevision []byte
	var previousSequence uint64
	err := transaction.QueryRowContext(ctx, `SELECT revision_id,author_sequence FROM revisions WHERE workspace_id=? AND author=? ORDER BY author_sequence DESC LIMIT 1`, core.WorkspaceID[:], nodeID[:]).Scan(&previousRevision, &previousSequence)
	if errors.Is(err, sql.ErrNoRows) {
		core.AuthorSequence = 1
		return nil
	}
	if err != nil {
		return err
	}
	if len(previousRevision) != len(core.PreviousAuthorRevision) {
		return errors.New("stored author revision has invalid length")
	}
	copy(core.PreviousAuthorRevision[:], previousRevision)
	core.AuthorSequence = previousSequence + 1
	return nil
}

func persistRevision(ctx context.Context, transaction *sql.Tx, workspace Workspace, expectedHead syncprotocol.RevisionID, revision builtRevision) error {
	coreBytes, err := syncprotocol.EncodeRevisionCore(revision.core)
	if err != nil {
		return err
	}
	var author any
	var authorSequence any
	if revision.core.Kind == syncprotocol.RevisionWrite {
		author = revision.core.Author[:]
		authorSequence = revision.core.AuthorSequence
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO revisions(revision_id,workspace_id,recovery_epoch,generation,kind,author,author_sequence,core) VALUES(?,?,?,?,?,?,?,?)`, revision.id[:], workspace.ID[:], workspace.Epoch[:], revision.core.Generation, revision.core.Kind, author, authorSequence, coreBytes); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO revision_parents(revision_id,position,parent_id) VALUES(?,?,?)`, revision.id[:], 0, expectedHead[:]); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO revision_proofs(revision_id,node_id,signature) VALUES(?,?,?)`, revision.id[:], revision.proof.NodeID[:], revision.proof.Signature); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM workspace_heads WHERE workspace_id=? AND revision_id=?`, workspace.ID[:], expectedHead[:])
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return ErrStaleHead
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, workspace.ID[:], revision.id[:])
	return err
}

func memberRole(snapshot syncprotocol.WorkspaceSnapshot, nodeID syncprotocol.NodeID) (syncprotocol.Role, bool) {
	for _, member := range snapshot.Members {
		if member.NodeID == nodeID {
			return member.Role, true
		}
	}
	return 0, false
}

func requiresAdmin(previous, next syncprotocol.RegistrySnapshot) bool {
	for name, oldProject := range previous.Projects {
		newProject, exists := next.Projects[name]
		if !exists || oldProject.Remote != newProject.Remote || !reflect.DeepEqual(oldProject.Mirrors, newProject.Mirrors) {
			return true
		}
	}
	for name, project := range next.Projects {
		if _, existed := previous.Projects[name]; !existed && (project.Remote != "" || len(project.Mirrors) != 0) {
			return true
		}
	}
	return false
}

func (store *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT w.workspace_id,w.recovery_epoch,w.name,w.root,h.revision_id,r.core FROM workspaces w JOIN workspace_heads h ON h.workspace_id=w.workspace_id JOIN revisions r ON r.revision_id=h.revision_id ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	for rows.Next() {
		workspace, err := store.scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func (store *Store) load(ctx context.Context, query string, argument any) (Workspace, error) {
	return store.scanWorkspace(store.db.QueryRowContext(ctx, query, argument))
}

func (store *Store) scanWorkspace(row scanner) (Workspace, error) {
	var workspace Workspace
	var workspaceID, epoch, head, coreBytes []byte
	if err := row.Scan(&workspaceID, &epoch, &workspace.Name, &workspace.Root, &head, &coreBytes); err != nil {
		return workspace, err
	}
	if len(workspaceID) != len(workspace.ID) || len(epoch) != len(workspace.Epoch) || len(head) != len(workspace.Head) {
		return workspace, errors.New("stored workspace identity has invalid length")
	}
	copy(workspace.ID[:], workspaceID)
	copy(workspace.Epoch[:], epoch)
	copy(workspace.Head[:], head)
	core, err := syncprotocol.DecodeRevisionCore(coreBytes)
	if err != nil {
		return workspace, err
	}
	snapshot, err := syncprotocol.DecodeWorkspaceSnapshot(core.Snapshot)
	if err != nil {
		return workspace, err
	}
	workspace.State = snapshot.Workspace(workspace.Root)
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

func withTx(ctx context.Context, database *sql.DB, run func(*sql.Tx) error) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = run(transaction); err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}
