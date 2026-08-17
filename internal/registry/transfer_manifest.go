package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type RevisionInventory struct {
	ID        string `json:"id"`
	WireBytes int64  `json:"wire_bytes"`
}

type RevisionManifest struct {
	WorkspaceID string              `json:"workspace_id"`
	Epoch       int64               `json:"epoch"`
	Heads       []string            `json:"heads"`
	Revisions   []RevisionInventory `json:"revisions"`
	Next        string              `json:"next,omitempty"`
}

type RevisionImportPlan struct {
	ID           string   `json:"id"`
	ManifestHash string   `json:"manifest_hash"`
	Missing      []string `json:"missing"`
	Next         string   `json:"next,omitempty"`
}

const maxRevisionBatchWireBytes = 16 << 20

var maxRevisionManifestPage = 10000

func (store *Store) Manifest(ctx context.Context, name string) (RevisionManifest, error) {
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return RevisionManifest{}, err
	}
	return store.workspaceManifest(ctx, workspace, false)
}

func (store *Store) ManifestFor(ctx context.Context, name, peerID string) (RevisionManifest, error) {
	workspace, err := store.authorizeWorkspacePeer(ctx, name, peerID)
	if err != nil {
		return RevisionManifest{}, err
	}
	return store.workspaceManifest(ctx, workspace, true)
}

func (store *Store) ManifestPageFor(ctx context.Context, name, peerID, after string) (RevisionManifest, error) {
	return store.ManifestPageForLimit(ctx, name, peerID, after, maxRevisionManifestPage)
}

func (store *Store) ManifestPageForLimit(ctx context.Context, name, peerID, after string, limit int) (RevisionManifest, error) {
	if limit < 1 || limit > maxRevisionManifestPage {
		return RevisionManifest{}, errors.New("revision manifest page limit is invalid")
	}
	workspace, err := store.authorizeWorkspacePeer(ctx, name, peerID)
	if err != nil {
		return RevisionManifest{}, err
	}
	return store.workspaceManifestPage(ctx, workspace, true, after, limit)
}

func (store *Store) workspaceManifest(ctx context.Context, workspace Workspace, shareable bool) (RevisionManifest, error) {
	return store.workspaceManifestPage(ctx, workspace, shareable, "", 0)
}

func (store *Store) workspaceManifestPage(ctx context.Context, workspace Workspace, shareable bool, after string, limit int) (RevisionManifest, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RevisionManifest{}, err
	}
	defer func() { _ = tx.Rollback() }()
	heads, err := loadHeadsFrom(ctx, tx, workspace.WorkspaceID)
	if err != nil {
		return RevisionManifest{}, err
	}
	ids, next, err := revisionIDsPage(ctx, tx, workspace.WorkspaceID, after, limit)
	if err != nil {
		return RevisionManifest{}, err
	}
	manifest := RevisionManifest{WorkspaceID: workspace.WorkspaceID, Epoch: workspace.Epoch, Heads: heads, Revisions: make([]RevisionInventory, 0, len(ids)), Next: next}
	manifest.Revisions, err = loadRevisionInventory(ctx, tx, workspace.WorkspaceID, ids, shareable)
	if err != nil {
		return RevisionManifest{}, err
	}
	if limit == 0 {
		err = validateRevisionManifest(manifest)
	} else {
		err = validateRevisionManifestPage(manifest)
	}
	if err != nil {
		return RevisionManifest{}, err
	}
	if err = tx.Commit(); err != nil {
		return RevisionManifest{}, err
	}
	return manifest, nil
}

func loadRevisionInventory(ctx context.Context, reader sqlReader, workspaceID string, ids []string, shareable bool) ([]RevisionInventory, error) {
	inventory := make([]RevisionInventory, 0, len(ids))
	for _, id := range ids {
		revision, loadErr := loadRevisionByID(ctx, reader, workspaceID, id)
		if loadErr != nil {
			return nil, loadErr
		}
		if shareable {
			if loadErr = validateShareableSnapshot(revision.Snapshot); loadErr != nil {
				return nil, loadErr
			}
		}
		body, marshalErr := json.Marshal(revision)
		if marshalErr != nil {
			return nil, marshalErr
		}
		inventory = append(inventory, RevisionInventory{ID: id, WireBytes: int64(len(body))})
	}
	return inventory, nil
}

func revisionIDsPage(ctx context.Context, reader sqlReader, workspaceID, after string, limit int) ([]string, string, error) {
	var count int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions WHERE workspace_id=?`, workspaceID).Scan(&count); err != nil {
		return nil, "", err
	}
	query := `SELECT id FROM revisions WHERE workspace_id=? AND id>? ORDER BY id`
	args := []any{workspaceID, after}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	capacity := count
	if limit > 0 && capacity > limit+1 {
		capacity = limit + 1
	}
	ids := make([]string, 0, capacity)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if limit > 0 && len(ids) > limit {
		next := ids[limit-1]
		return ids[:limit], next, nil
	}
	return ids, "", nil
}

func loadRevisionByID(ctx context.Context, reader sqlReader, workspaceID, id string) (Revision, error) {
	var revision Revision
	var conflicts, access []byte
	revision.WorkspaceID = workspaceID
	err := reader.QueryRowContext(ctx, `SELECT id,epoch,kind,snapshot,conflicts,access,network_head FROM revisions WHERE id=? AND workspace_id=?`, id, workspaceID).Scan(&revision.ID, &revision.Epoch, &revision.Kind, &revision.Snapshot, &conflicts, &access, &revision.NetworkHead)
	if err != nil {
		return Revision{}, err
	}
	if err = json.Unmarshal(conflicts, &revision.Conflicts); err != nil {
		return Revision{}, err
	}
	if len(access) > 0 && string(access) != "null" {
		var policy AccessPolicy
		if err = json.Unmarshal(access, &policy); err != nil {
			return Revision{}, err
		}
		revision.Access = &policy
	}
	revision.Parents, err = loadParentsFrom(ctx, reader, id)
	if err != nil {
		return Revision{}, err
	}
	revision.Proofs, err = loadProofsFrom(ctx, reader, id)
	return revision, err
}

func (store *Store) RevisionsFor(ctx context.Context, name, peerID string, ids []string) ([]Revision, error) {
	if len(ids) == 0 || len(ids) > maxImportBatchRevisions {
		return nil, errors.New("revision request count is invalid")
	}
	workspace, err := store.authorizeWorkspacePeer(ctx, name, peerID)
	if err != nil {
		return nil, err
	}
	if err = validateRevisionIDs(ids); err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	revisions := make([]Revision, 0, len(ids))
	var wireBytes int64
	for _, id := range ids {
		revision, loadErr := loadRevisionByID(ctx, tx, workspace.WorkspaceID, id)
		if loadErr != nil {
			return nil, loadErr
		}
		if loadErr = validateShareableSnapshot(revision.Snapshot); loadErr != nil {
			return nil, loadErr
		}
		size, sizeErr := revisionWireBytes(revision)
		if sizeErr != nil {
			return nil, sizeErr
		}
		wireBytes += size
		if wireBytes >= maxRevisionBatchWireBytes {
			return nil, errors.New("revision response batch is oversized")
		}
		revisions = append(revisions, revision)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func validateRevisionManifest(manifest RevisionManifest) error {
	if manifest.Next != "" {
		return errors.New("revision manifest is incomplete")
	}
	if manifest.WorkspaceID == "" || manifest.Epoch < 1 || len(manifest.Heads) == 0 {
		return errors.New("revision manifest identity is invalid")
	}
	if len(manifest.Heads) > maxBundleHeads {
		return errors.New("revision manifest history limit exceeded")
	}
	indexed, err := validateRevisionInventory(manifest.Revisions)
	if err != nil {
		return err
	}
	return validateManifestHeads(manifest.Heads, indexed)
}

func validateRevisionManifestPage(manifest RevisionManifest) error {
	if manifest.WorkspaceID == "" || manifest.Epoch < 1 || len(manifest.Heads) == 0 {
		return errors.New("revision manifest identity is invalid")
	}
	if len(manifest.Revisions) > maxRevisionManifestPage || len(manifest.Heads) > maxBundleHeads {
		return errors.New("revision manifest history limit exceeded")
	}
	_, err := validateRevisionInventory(manifest.Revisions)
	if err != nil {
		return err
	}
	if err = validateOrderedRevisionIDs(manifest.Heads); err != nil {
		return err
	}
	if manifest.Next != "" && (!validRevisionID(manifest.Next) || len(manifest.Revisions) == 0 || manifest.Revisions[len(manifest.Revisions)-1].ID != manifest.Next) {
		return errors.New("revision manifest cursor is invalid")
	}
	return nil
}

func validateOrderedRevisionIDs(ids []string) error {
	for index, id := range ids {
		if !validRevisionID(id) || index > 0 && ids[index-1] >= id {
			return errors.New("revision manifest head set is invalid")
		}
	}
	return nil
}

func validateRevisionInventory(revisions []RevisionInventory) (map[string]bool, error) {
	indexed := make(map[string]bool, len(revisions))
	for index, revision := range revisions {
		if !validRevisionID(revision.ID) || revision.WireBytes < 1 || index > 0 && revisions[index-1].ID >= revision.ID {
			return nil, errors.New("revision manifest inventory is invalid")
		}
		indexed[revision.ID] = true
	}
	return indexed, nil
}

func validateManifestHeads(heads []string, indexed map[string]bool) error {
	for index, head := range heads {
		if !validRevisionID(head) || !indexed[head] || index > 0 && heads[index-1] >= head {
			return errors.New("revision manifest head set is invalid")
		}
	}
	return nil
}

func validateRevisionIDs(ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validRevisionID(id) {
			return errors.New("revision ID is malformed")
		}
		if seen[id] {
			return errors.New("revision request contains a duplicate ID")
		}
		seen[id] = true
	}
	return nil
}

func validRevisionID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(id)
	return err == nil && hex.EncodeToString(decoded) == id
}

func revisionManifestHash(manifest RevisionManifest) (string, error) {
	if err := validateRevisionManifest(manifest); err != nil {
		return "", err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func sortedManifestRevisions(revisions map[string]Revision) []Revision {
	ids := make([]string, 0, len(revisions))
	for id := range revisions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Revision, 0, len(ids))
	for _, id := range ids {
		result = append(result, revisions[id])
	}
	return result
}

func revisionWireBytes(revision Revision) (int64, error) {
	body, err := json.Marshal(revision)
	if err != nil {
		return 0, fmt.Errorf("encode revision %s: %w", revision.ID, err)
	}
	return int64(len(body)), nil
}
