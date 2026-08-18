package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kuchmenko/workspace/internal/config"
)

func (store *Store) FinishAttachImport(ctx context.Context, importID, peerID, workspaceID, manifestHash string) (Workspace, error) {
	item, manifest, head, err := store.prepareAttachImport(ctx, importID, peerID, workspaceID, manifestHash)
	if err != nil {
		return Workspace{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = store.loadRevisionImport(ctx, tx, importID, peerID, workspaceID, RevisionImportAttach, manifestHash); err != nil {
		return Workspace{}, err
	}
	if _, err = loadImportManifest(ctx, tx, item); err != nil {
		return Workspace{}, err
	}
	if err = requireCompleteImport(ctx, tx, item.id); err != nil {
		return Workspace{}, err
	}
	if err = publishImportedRevisions(ctx, tx, item.id, item.workspaceID); err != nil {
		return Workspace{}, err
	}
	state, err := loadRevisionState(tx, head)
	if err != nil {
		return Workspace{}, err
	}
	if err = persistImportedAttachment(ctx, tx, item, manifest, state, head); err != nil {
		return Workspace{}, err
	}
	if err = deleteRevisionImport(ctx, tx, item.id); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, item.workspaceName)
}

func (store *Store) prepareAttachImport(ctx context.Context, importID, peerID, workspaceID, manifestHash string) (revisionImport, RevisionManifest, string, error) {
	devices, active, network, err := store.importAuthorization(ctx)
	if err != nil {
		return revisionImport{}, RevisionManifest{}, "", err
	}
	item, err := store.loadRevisionImport(ctx, store.db, importID, peerID, workspaceID, RevisionImportAttach, manifestHash)
	if err != nil {
		return revisionImport{}, RevisionManifest{}, "", err
	}
	manifest, revisions, _, heads, err := prepareImportedHistory(ctx, store.db, item, devices, network)
	if err != nil {
		return revisionImport{}, RevisionManifest{}, "", store.abortImport(item, err)
	}
	if len(heads) != 1 {
		return revisionImport{}, RevisionManifest{}, "", store.abortImport(item, errors.New("cannot attach a workspace with unresolved divergent heads"))
	}
	policy := revisionPolicyMap(revisions, heads[0])
	if policy.Role(store.identity.ID(), active[store.identity.ID()]) == "" || policy.Role(peerID, active[peerID]) == "" {
		return revisionImport{}, RevisionManifest{}, "", store.abortImport(item, errors.New("local device or workspace source is not authorized"))
	}
	return item, manifest, heads[0], nil
}

func persistImportedAttachment(ctx context.Context, tx *sql.Tx, item revisionImport, manifest RevisionManifest, state *config.Workspace, head string) error {
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,1,?)`, item.workspaceName, item.root, body); err != nil {
		return fmt.Errorf("attach workspace: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,?,?)`, item.workspaceName, manifest.WorkspaceID, manifest.Epoch, head); err != nil {
		return err
	}
	if err = replaceHeads(ctx, tx, manifest.WorkspaceID, manifest.Heads); err != nil {
		return err
	}
	conflicts, err := loadRevisionConflicts(tx, head)
	if err != nil {
		return err
	}
	return replaceConflicts(ctx, tx, manifest.WorkspaceID, head, conflicts)
}

func (store *Store) FinishSyncImport(ctx context.Context, importID, peerID, workspaceID, manifestHash string) (Workspace, []Conflict, []string, error) {
	devices, active, network, err := store.importAuthorization(ctx)
	if err != nil {
		return Workspace{}, nil, nil, err
	}
	item, err := store.loadRevisionImport(ctx, store.db, importID, peerID, workspaceID, RevisionImportSync, manifestHash)
	if err != nil {
		return Workspace{}, nil, nil, err
	}
	manifest, revisions, _, heads, err := prepareImportedHistory(ctx, store.db, item, devices, network)
	if err != nil {
		return Workspace{}, nil, heads, store.abortImport(item, err)
	}
	bundle := Bundle{WorkspaceID: manifest.WorkspaceID, Epoch: manifest.Epoch, Heads: heads, Revisions: sortedManifestRevisions(revisions)}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = store.loadRevisionImport(ctx, tx, importID, peerID, workspaceID, RevisionImportSync, manifestHash); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	if _, err = loadImportManifest(ctx, tx, item); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	if err = requireCompleteImport(ctx, tx, item.id); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	base, err := loadIncomingBase(ctx, tx, item.workspaceName)
	if err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	if err = acceptBundleEpoch(ctx, tx, bundle, peerID, true, base.epoch); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	if err = publishImportedRevisions(ctx, tx, item.id, item.workspaceID); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	if err = deleteRevisionImport(ctx, tx, item.id); err != nil {
		return Workspace{}, nil, heads, store.abortFailedImport(tx, item, err)
	}
	conflicts, err := store.persistIncomingTx(ctx, tx, item.workspaceName, bundle, peerID, true, active, heads, false)
	if err != nil {
		return Workspace{}, conflicts, heads, store.abortFailedImport(tx, item, err)
	}
	workspace, err := store.LoadByName(ctx, item.workspaceName)
	return workspace, conflicts, heads, err
}

func prepareImportedHistory(ctx context.Context, reader sqlReader, item revisionImport, devices map[string]DeviceRecord, network NetworkBundle) (RevisionManifest, map[string]Revision, map[string]bool, []string, error) {
	manifest, err := loadImportManifest(ctx, reader, item)
	if err != nil {
		return RevisionManifest{}, nil, nil, nil, err
	}
	if err = requireCompleteImport(ctx, reader, item.id); err != nil {
		return RevisionManifest{}, nil, nil, nil, err
	}
	revisions, staged, err := loadImportedRevisions(ctx, reader, item, manifest)
	if err != nil {
		return RevisionManifest{}, nil, nil, nil, err
	}
	heads, err := validateImportedHistory(ctx, reader, item, manifest, revisions, staged, devices, network)
	return manifest, revisions, staged, heads, err
}

func (store *Store) importAuthorization(ctx context.Context) (map[string]DeviceRecord, map[string]bool, NetworkBundle, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return nil, nil, NetworkBundle{}, err
	}
	devices := make(map[string]DeviceRecord, len(state.Devices))
	active := make(map[string]bool, len(state.Devices))
	for _, record := range state.Devices {
		devices[record.ID] = record
		active[record.ID] = record.Active
	}
	network, err := store.ExportNetwork(ctx)
	return devices, active, network, err
}

func publishImportedRevisions(ctx context.Context, tx *sql.Tx, importID, workspaceID string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO revisions(id,workspace_id,epoch,kind,snapshot,conflicts,access,network_head) SELECT id,workspace_id,epoch,kind,snapshot,conflicts,access,network_head FROM workspace_import_revisions WHERE import_id=?`, importID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO revision_parents(revision_id,parent_id,position) SELECT revision_id,parent_id,position FROM workspace_import_parents WHERE import_id=?`, importID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO revision_proofs(revision_id,device_id,public_key,signature) SELECT revision_id,device_id,public_key,signature FROM workspace_import_proofs WHERE import_id=?`, importID)
	if err != nil {
		return err
	}
	return validateStoredParents(tx, workspaceID)
}

func (store *Store) abortFailedImport(tx *sql.Tx, item revisionImport, cause error) error {
	_ = tx.Rollback()
	return store.abortImport(item, cause)
}

func (store *Store) abortImport(item revisionImport, cause error) error {
	abortErr := store.AbortRevisionImport(context.Background(), item.id, item.peerID, item.workspaceID, item.mode, item.manifestHash)
	if abortErr != nil {
		return errors.Join(cause, fmt.Errorf("abort revision import: %w", abortErr))
	}
	return cause
}
