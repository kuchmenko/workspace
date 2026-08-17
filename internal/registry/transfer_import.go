package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	RevisionImportAttach    = "attach"
	RevisionImportSync      = "sync"
	maxImportBatchRevisions = 128
	revisionImportLifetime  = time.Hour
)

var errRevisionImportExpired = errors.New("revision import expired")

type revisionImport struct {
	id, peerID, workspaceID, mode, manifestHash string
	workspaceName, root                         string
	epoch                                       int64
	heads                                       []string
	expiresAt                                   int64
}

func (store *Store) BeginAttachImport(ctx context.Context, name, root, peerID string, manifest RevisionManifest) (RevisionImportPlan, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return RevisionImportPlan{}, errors.New("workspace name is required")
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	if peerID == "" || !active[peerID] {
		return RevisionImportPlan{}, errors.New("workspace source is not an active network device")
	}
	return store.beginRevisionImport(ctx, name, canonical, peerID, RevisionImportAttach, manifest)
}

func (store *Store) BeginSyncImport(ctx context.Context, name, peerID string, manifest RevisionManifest) (RevisionImportPlan, error) {
	workspace, err := store.authorizeWorkspacePeer(ctx, name, peerID)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	if workspace.WorkspaceID != manifest.WorkspaceID {
		return RevisionImportPlan{}, errors.New("workspace ID does not match")
	}
	return store.beginRevisionImport(ctx, workspace.Name, "", peerID, RevisionImportSync, manifest)
}

func (store *Store) beginRevisionImport(ctx context.Context, name, root, peerID, mode string, manifest RevisionManifest) (RevisionImportPlan, error) {
	hash, err := revisionManifestHash(manifest)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	heads, err := json.Marshal(manifest.Heads)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	item := revisionImport{
		id: uuid.NewString(), peerID: peerID, workspaceID: manifest.WorkspaceID, mode: mode, manifestHash: hash,
		workspaceName: name, root: root, epoch: manifest.Epoch, heads: append([]string(nil), manifest.Heads...),
		expiresAt: time.Now().Add(revisionImportLifetime).Unix(),
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = purgeExpiredImports(ctx, tx); err != nil {
		return RevisionImportPlan{}, err
	}
	if err = deleteBoundImports(ctx, tx, peerID, manifest.WorkspaceID, mode); err != nil {
		return RevisionImportPlan{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_imports(id,peer_id,workspace_id,mode,manifest_hash,workspace_name,root,epoch,heads,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.id, peerID, manifest.WorkspaceID, mode, hash, name, root, manifest.Epoch, heads, item.expiresAt)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	missing := make([]string, 0, len(manifest.Revisions))
	for _, revision := range manifest.Revisions {
		requested, requestErr := importRequestsRevision(ctx, tx, mode, manifest.WorkspaceID, revision.ID)
		if requestErr != nil {
			return RevisionImportPlan{}, requestErr
		}
		if requested {
			missing = append(missing, revision.ID)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_import_manifest(import_id,revision_id,wire_bytes,requested) VALUES(?,?,?,?)`, item.id, revision.ID, revision.WireBytes, requested)
		if err != nil {
			return RevisionImportPlan{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return RevisionImportPlan{}, err
	}
	return RevisionImportPlan{ID: item.id, ManifestHash: hash, Missing: missing}, nil
}

func importRequestsRevision(ctx context.Context, tx *sql.Tx, mode, workspaceID, revisionID string) (bool, error) {
	if mode == RevisionImportAttach {
		return true, nil
	}
	var storedWorkspace string
	err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM revisions WHERE id=?`, revisionID).Scan(&storedWorkspace)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if storedWorkspace != workspaceID {
		return false, errors.New("revision ID belongs to another workspace")
	}
	return false, nil
}

func (store *Store) StageRevisionImport(ctx context.Context, importID, peerID, workspaceID, mode, manifestHash string, revisions []Revision) error {
	if len(revisions) == 0 || len(revisions) > maxImportBatchRevisions {
		return errors.New("revision import batch count is invalid")
	}
	item, err := store.loadRevisionImport(ctx, store.db, importID, peerID, workspaceID, mode, manifestHash)
	if err != nil {
		return err
	}
	if err = store.validateRevisionImportBatch(ctx, item, revisions); err != nil {
		return err
	}
	return store.persistRevisionImportBatch(ctx, item, revisions)
}

func (store *Store) validateRevisionImportBatch(ctx context.Context, item revisionImport, revisions []Revision) error {
	seen := make(map[string]Revision, len(revisions))
	for _, revision := range revisions {
		if err := store.validateRevisionImport(ctx, item, revision, seen); err != nil {
			return err
		}
		seen[revision.ID] = revision
	}
	return nil
}

func (store *Store) validateRevisionImport(ctx context.Context, item revisionImport, revision Revision, seen map[string]Revision) error {
	if !validRevisionID(revision.ID) {
		return errors.New("revision ID is malformed")
	}
	for _, parent := range revision.Parents {
		if !validRevisionID(parent) {
			return errors.New("revision parent ID is malformed")
		}
	}
	if _, duplicate := seen[revision.ID]; duplicate {
		return errors.New("revision import batch contains a duplicate")
	}
	if err := store.requireRequestedRevision(ctx, store.db, item.id, revision.ID); err != nil {
		return err
	}
	if err := validateBundledRevision(Bundle{WorkspaceID: item.workspaceID, Epoch: item.epoch}, revision, seen); err != nil {
		return err
	}
	if err := validateShareableSnapshot(revision.Snapshot); err != nil {
		return err
	}
	wireBytes, err := revisionWireBytes(revision)
	if err != nil {
		return err
	}
	return store.requireRevisionWireBytes(ctx, store.db, item.id, revision.ID, wireBytes)
}

func (store *Store) persistRevisionImportBatch(ctx context.Context, item revisionImport, revisions []Revision) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = store.loadRevisionImport(ctx, tx, item.id, item.peerID, item.workspaceID, item.mode, item.manifestHash); err != nil {
		return err
	}
	for _, revision := range revisions {
		if err = store.requireRequestedRevision(ctx, tx, item.id, revision.ID); err != nil {
			return err
		}
		if err = persistStagedRevision(ctx, tx, item.id, revision); err != nil {
			return err
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE workspace_import_manifest SET received=1 WHERE import_id=? AND revision_id=? AND requested=1 AND received=0`, item.id, revision.ID)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("revision import batch contains a duplicate or unrequested revision")
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_imports SET expires_at=? WHERE id=?`, time.Now().Add(revisionImportLifetime).Unix(), item.id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func persistStagedRevision(ctx context.Context, tx *sql.Tx, importID string, revision Revision) error {
	conflicts, err := json.Marshal(revision.Conflicts)
	if err != nil {
		return err
	}
	access, err := json.Marshal(revision.Access)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_import_revisions(import_id,id,workspace_id,epoch,kind,snapshot,conflicts,access,network_head) VALUES(?,?,?,?,?,?,?,?,?)`, importID, revision.ID, revision.WorkspaceID, revision.Epoch, revision.Kind, revision.Snapshot, conflicts, access, revision.NetworkHead)
	if err != nil {
		return err
	}
	for position, parent := range revision.Parents {
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_import_parents(import_id,revision_id,parent_id,position) VALUES(?,?,?,?)`, importID, revision.ID, parent, position); err != nil {
			return err
		}
	}
	for _, proof := range revision.Proofs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_import_proofs(import_id,revision_id,device_id,public_key,signature) VALUES(?,?,?,?,?)`, importID, revision.ID, proof.DeviceID, proof.PublicKey, proof.Signature); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) requireRequestedRevision(ctx context.Context, reader sqlReader, importID, revisionID string) error {
	var requested, received int
	err := reader.QueryRowContext(ctx, `SELECT requested,received FROM workspace_import_manifest WHERE import_id=? AND revision_id=?`, importID, revisionID).Scan(&requested, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("revision import contains an undeclared revision")
	}
	if err != nil {
		return err
	}
	if requested == 0 {
		return errors.New("revision import contains an unrequested revision")
	}
	if received != 0 {
		return errors.New("revision import contains a duplicate revision")
	}
	return nil
}

func (store *Store) requireRevisionWireBytes(ctx context.Context, reader sqlReader, importID, revisionID string, actual int64) error {
	var declared int64
	if err := reader.QueryRowContext(ctx, `SELECT wire_bytes FROM workspace_import_manifest WHERE import_id=? AND revision_id=?`, importID, revisionID).Scan(&declared); err != nil {
		return err
	}
	if actual != declared {
		return errors.New("revision import size does not match manifest")
	}
	return nil
}

func (store *Store) loadRevisionImport(ctx context.Context, reader sqlReader, importID, peerID, workspaceID, mode, manifestHash string) (revisionImport, error) {
	var item revisionImport
	var heads []byte
	err := reader.QueryRowContext(ctx, `SELECT id,peer_id,workspace_id,mode,manifest_hash,workspace_name,root,epoch,heads,expires_at FROM workspace_imports WHERE id=?`, importID).Scan(&item.id, &item.peerID, &item.workspaceID, &item.mode, &item.manifestHash, &item.workspaceName, &item.root, &item.epoch, &heads, &item.expiresAt)
	if err != nil {
		return item, err
	}
	if item.peerID != peerID || item.workspaceID != workspaceID || item.mode != mode || item.manifestHash != manifestHash {
		return revisionImport{}, errors.New("revision import binding does not match")
	}
	if item.expiresAt <= time.Now().Unix() {
		return item, errRevisionImportExpired
	}
	if err = json.Unmarshal(heads, &item.heads); err != nil {
		return revisionImport{}, err
	}
	return item, nil
}

func (store *Store) AbortRevisionImport(ctx context.Context, importID, peerID, workspaceID, mode, manifestHash string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := store.loadRevisionImport(ctx, tx, importID, peerID, workspaceID, mode, manifestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil && !errors.Is(err, errRevisionImportExpired) {
		return err
	}
	if err = deleteRevisionImport(ctx, tx, item.id); err != nil {
		return err
	}
	return tx.Commit()
}

func purgeExpiredImports(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM workspace_imports WHERE expires_at<=?`, time.Now().Unix())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = deleteRevisionImport(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func deleteBoundImports(ctx context.Context, tx *sql.Tx, peerID, workspaceID, mode string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM workspace_imports WHERE peer_id=? AND workspace_id=? AND mode=?`, peerID, workspaceID, mode)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = deleteRevisionImport(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func deleteRevisionImport(ctx context.Context, tx *sql.Tx, importID string) error {
	queries := []string{
		`DELETE FROM workspace_import_proofs WHERE import_id=?`,
		`DELETE FROM workspace_import_parents WHERE import_id=?`,
		`DELETE FROM workspace_import_revisions WHERE import_id=?`,
		`DELETE FROM workspace_import_manifest WHERE import_id=?`,
		`DELETE FROM workspace_imports WHERE id=?`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, importID); err != nil {
			return err
		}
	}
	return nil
}
