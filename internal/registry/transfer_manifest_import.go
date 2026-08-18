package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"time"

	"github.com/google/uuid"
)

type revisionManifestImport struct {
	peerID, workspaceID, mode, next string
	epoch                           int64
	heads                           []string
	finished                        bool
	expiresAt                       int64
}

func (store *Store) BeginAttachImportPage(ctx context.Context, name, root, peerID string, page RevisionManifest) (string, error) {
	name, err := validateWorkspaceName(name)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return "", err
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return "", err
	}
	if peerID == "" || !active[peerID] {
		return "", errors.New("workspace source is not an active network device")
	}
	return store.beginRevisionImportPage(ctx, name, canonical, peerID, RevisionImportAttach, page)
}

func (store *Store) BeginSyncImportPage(ctx context.Context, name, peerID string, page RevisionManifest) (string, error) {
	workspace, err := store.authorizeWorkspacePeer(ctx, name, peerID)
	if err != nil {
		return "", err
	}
	if workspace.WorkspaceID != page.WorkspaceID {
		return "", errors.New("workspace ID does not match")
	}
	return store.beginRevisionImportPage(ctx, workspace.Name, "", peerID, RevisionImportSync, page)
}

func (store *Store) beginRevisionImportPage(ctx context.Context, name, root, peerID, mode string, page RevisionManifest) (string, error) {
	if err := validateRevisionManifestPage(page); err != nil {
		return "", err
	}
	heads, err := json.Marshal(page.Heads)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err = purgeExpiredImports(ctx, tx); err != nil {
		return "", err
	}
	if err = deleteBoundImports(ctx, tx, peerID, page.WorkspaceID, mode); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_imports(id,peer_id,workspace_id,mode,manifest_hash,workspace_name,root,epoch,heads,manifest_next,manifest_finished,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,0,?)`, id, peerID, page.WorkspaceID, mode, "", name, root, page.Epoch, heads, "", time.Now().Add(revisionImportLifetime).Unix())
	if err != nil {
		return "", err
	}
	if err = appendManifestPage(ctx, tx, id, mode, page, ""); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (store *Store) AppendRevisionImportManifest(ctx context.Context, importID, peerID, workspaceID, mode, after string, page RevisionManifest) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := loadRevisionManifestImport(ctx, tx, importID)
	if err != nil {
		return err
	}
	if err = validateManifestImportAppend(item, peerID, workspaceID, mode, after, page); err != nil {
		return err
	}
	if err = appendManifestPage(ctx, tx, importID, mode, page, item.next); err != nil {
		return err
	}
	return tx.Commit()
}

func appendManifestPage(ctx context.Context, tx *sql.Tx, importID, mode string, page RevisionManifest, expectedAfter string) error {
	if err := validateRevisionManifestPage(page); err != nil {
		return err
	}
	if expectedAfter != "" && (len(page.Revisions) == 0 || page.Revisions[0].ID <= expectedAfter) {
		return errors.New("revision manifest cursor is not continuous")
	}
	for _, revision := range page.Revisions {
		requested, err := importRequestsRevision(ctx, tx, mode, page.WorkspaceID, revision.ID)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_import_manifest(import_id,revision_id,wire_bytes,requested) VALUES(?,?,?,?)`, importID, revision.ID, revision.WireBytes, requested); err != nil {
			return errors.New("revision manifest contains a duplicate or out-of-order page")
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE workspace_imports SET manifest_next=?,expires_at=? WHERE id=?`, page.Next, time.Now().Add(revisionImportLifetime).Unix(), importID)
	return err
}

func (store *Store) FinishRevisionImportManifest(ctx context.Context, importID, peerID, workspaceID, mode string) (RevisionImportPlan, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := loadRevisionManifestImport(ctx, tx, importID)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	if err = validateManifestImportFinish(item, peerID, workspaceID, mode); err != nil {
		return RevisionImportPlan{}, err
	}
	if err = validateStagedManifestHeads(ctx, tx, importID); err != nil {
		return RevisionImportPlan{}, err
	}
	hashValue, err := hashStagedManifest(ctx, tx, importID)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_imports SET manifest_hash=?,manifest_finished=1 WHERE id=?`, hashValue, importID); err != nil {
		return RevisionImportPlan{}, err
	}
	plan, err := missingImportPage(ctx, tx, importID, hashValue, "")
	if err != nil {
		return RevisionImportPlan{}, err
	}
	if err = tx.Commit(); err != nil {
		return RevisionImportPlan{}, err
	}
	return plan, nil
}

func loadRevisionManifestImport(ctx context.Context, reader sqlReader, importID string) (revisionManifestImport, error) {
	var item revisionManifestImport
	var heads []byte
	var finished int
	err := reader.QueryRowContext(ctx, `SELECT peer_id,workspace_id,mode,epoch,heads,manifest_next,manifest_finished,expires_at FROM workspace_imports WHERE id=?`, importID).Scan(&item.peerID, &item.workspaceID, &item.mode, &item.epoch, &heads, &item.next, &finished, &item.expiresAt)
	if err != nil {
		return revisionManifestImport{}, err
	}
	if err = json.Unmarshal(heads, &item.heads); err != nil {
		return revisionManifestImport{}, err
	}
	item.finished = finished != 0
	return item, nil
}

func validateManifestImportAppend(item revisionManifestImport, peerID, workspaceID, mode, after string, page RevisionManifest) error {
	if item.peerID != peerID || item.workspaceID != workspaceID || item.mode != mode {
		return errors.New("revision import binding does not match")
	}
	if item.expiresAt <= time.Now().Unix() {
		return errRevisionImportExpired
	}
	if inconsistentManifestImportAppend(item, workspaceID, after, page) {
		return errors.New("revision manifest page is inconsistent")
	}
	return nil
}

func inconsistentManifestImportAppend(item revisionManifestImport, workspaceID, after string, page RevisionManifest) bool {
	return item.finished || item.next == "" || after != item.next || page.WorkspaceID != workspaceID || page.Epoch != item.epoch || !equalStringLists(page.Heads, item.heads)
}

func validateManifestImportFinish(item revisionManifestImport, peerID, workspaceID, mode string) error {
	if item.peerID != peerID || item.workspaceID != workspaceID || item.mode != mode {
		return errors.New("revision import binding does not match")
	}
	if item.expiresAt <= time.Now().Unix() {
		return errRevisionImportExpired
	}
	if item.finished || item.next != "" {
		return errors.New("revision manifest is incomplete")
	}
	return nil
}

func validateStagedManifestHeads(ctx context.Context, reader sqlReader, importID string) error {
	var heads []byte
	if err := reader.QueryRowContext(ctx, `SELECT heads FROM workspace_imports WHERE id=?`, importID).Scan(&heads); err != nil {
		return err
	}
	var ids []string
	if err := json.Unmarshal(heads, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		var found int
		if err := reader.QueryRowContext(ctx, `SELECT 1 FROM workspace_import_manifest WHERE import_id=? AND revision_id=?`, importID, id).Scan(&found); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("revision manifest head set is invalid")
			}
			return err
		}
	}
	return nil
}

func (store *Store) RevisionImportMissingPage(ctx context.Context, importID, peerID, workspaceID, mode, manifestHash, after string) (RevisionImportPlan, error) {
	if _, err := store.loadRevisionImport(ctx, store.db, importID, peerID, workspaceID, mode, manifestHash); err != nil {
		return RevisionImportPlan{}, err
	}
	return missingImportPage(ctx, store.db, importID, manifestHash, after)
}

func missingImportPage(ctx context.Context, reader sqlReader, importID, manifestHash, after string) (RevisionImportPlan, error) {
	rows, err := reader.QueryContext(ctx, `SELECT revision_id,wire_bytes FROM workspace_import_manifest WHERE import_id=? AND requested=1 AND revision_id>? ORDER BY revision_id LIMIT ?`, importID, after, maxImportBatchRevisions+1)
	if err != nil {
		return RevisionImportPlan{}, err
	}
	defer func() { _ = rows.Close() }()
	plan := RevisionImportPlan{ID: importID, ManifestHash: manifestHash}
	var bytes int64
	for rows.Next() {
		var id string
		var size int64
		if err = rows.Scan(&id, &size); err != nil {
			return RevisionImportPlan{}, err
		}
		if len(plan.Missing) == maxImportBatchRevisions || len(plan.Missing) > 0 && bytes+size >= maxRevisionBatchWireBytes {
			plan.Next = plan.Missing[len(plan.Missing)-1]
			break
		}
		if size < 1 || size >= maxRevisionBatchWireBytes {
			return RevisionImportPlan{}, errors.New("revision manifest inventory is invalid")
		}
		plan.Missing = append(plan.Missing, id)
		bytes += size
	}
	return plan, rows.Err()
}

func hashStagedManifest(ctx context.Context, reader sqlReader, importID string) (string, error) {
	var workspaceID string
	var epoch int64
	var heads json.RawMessage
	if err := reader.QueryRowContext(ctx, `SELECT workspace_id,epoch,heads FROM workspace_imports WHERE id=?`, importID).Scan(&workspaceID, &epoch, &heads); err != nil {
		return "", err
	}
	digest := sha256.New()
	prefix, _ := json.Marshal(struct {
		WorkspaceID string          `json:"workspace_id"`
		Epoch       int64           `json:"epoch"`
		Heads       json.RawMessage `json:"heads"`
	}{workspaceID, epoch, heads})
	digest.Write(prefix[:len(prefix)-1])
	digest.Write([]byte(`,"revisions":[`))
	rows, err := reader.QueryContext(ctx, `SELECT revision_id,wire_bytes FROM workspace_import_manifest WHERE import_id=? ORDER BY revision_id`, importID)
	if err != nil {
		return "", err
	}
	first := true
	for rows.Next() {
		var item RevisionInventory
		if err = rows.Scan(&item.ID, &item.WireBytes); err != nil {
			_ = rows.Close()
			return "", err
		}
		writeInventoryHash(digest, item, first)
		first = false
	}
	if err = rows.Close(); err != nil {
		return "", err
	}
	digest.Write([]byte(`]}`))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeInventoryHash(digest hash.Hash, item RevisionInventory, first bool) {
	if !first {
		digest.Write([]byte{','})
	}
	body, _ := json.Marshal(item)
	digest.Write(body)
}

func equalStringLists(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
