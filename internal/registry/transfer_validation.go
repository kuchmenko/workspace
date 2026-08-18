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

func loadImportManifest(ctx context.Context, reader sqlReader, item revisionImport) (RevisionManifest, error) {
	var count int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_import_manifest WHERE import_id=?`, item.id).Scan(&count); err != nil {
		return RevisionManifest{}, err
	}
	rows, err := reader.QueryContext(ctx, `SELECT revision_id,wire_bytes FROM workspace_import_manifest WHERE import_id=? ORDER BY revision_id`, item.id)
	if err != nil {
		return RevisionManifest{}, err
	}
	manifest := RevisionManifest{WorkspaceID: item.workspaceID, Epoch: item.epoch, Heads: append([]string(nil), item.heads...), Revisions: make([]RevisionInventory, 0, count)}
	for rows.Next() {
		var revision RevisionInventory
		if err = rows.Scan(&revision.ID, &revision.WireBytes); err != nil {
			_ = rows.Close()
			return RevisionManifest{}, err
		}
		manifest.Revisions = append(manifest.Revisions, revision)
	}
	if err = rows.Close(); err != nil {
		return RevisionManifest{}, err
	}
	hash, err := revisionManifestHash(manifest)
	if err != nil {
		return RevisionManifest{}, err
	}
	if hash != item.manifestHash {
		return RevisionManifest{}, errors.New("revision import manifest changed")
	}
	return manifest, nil
}

func requireCompleteImport(ctx context.Context, reader sqlReader, importID string) error {
	var missing int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_import_manifest WHERE import_id=? AND requested=1 AND received=0`, importID).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return errors.New("revision import is incomplete")
	}
	var extra int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_import_revisions r LEFT JOIN workspace_import_manifest m ON m.import_id=r.import_id AND m.revision_id=r.id WHERE r.import_id=? AND (m.revision_id IS NULL OR m.requested=0 OR m.received=0)`, importID).Scan(&extra); err != nil {
		return err
	}
	if extra != 0 {
		return errors.New("revision import contains undeclared data")
	}
	return nil
}

func loadImportedRevisions(ctx context.Context, reader sqlReader, item revisionImport, manifest RevisionManifest) (map[string]Revision, map[string]bool, error) {
	revisions := make(map[string]Revision, len(manifest.Revisions))
	staged := make(map[string]bool, len(manifest.Revisions))
	for _, inventory := range manifest.Revisions {
		revision, isStaged, err := loadImportedRevision(ctx, reader, item, inventory.ID)
		if err != nil {
			return nil, nil, err
		}
		revisions[revision.ID] = revision
		staged[revision.ID] = isStaged
	}
	return revisions, staged, nil
}

func loadImportedRevision(ctx context.Context, reader sqlReader, item revisionImport, id string) (Revision, bool, error) {
	revision, snapshot, conflicts, access, err := loadStagedRevision(ctx, reader, item, id)
	staged := err == nil
	if errors.Is(err, sql.ErrNoRows) && item.mode == RevisionImportSync {
		revision, snapshot, conflicts, access, err = loadLiveRevisionMetadata(ctx, reader, item.workspaceID, id)
	}
	if err != nil {
		return Revision{}, false, err
	}
	snapshotDigest := sha256.Sum256(snapshot)
	conflictDigest := sha256.Sum256(conflicts)
	conflictHash, _ := json.Marshal(hex.EncodeToString(conflictDigest[:]))
	revision.Snapshot = append([]byte(nil), snapshotDigest[:]...)
	revision.Conflicts = []Conflict{{Base: conflictHash}}
	if len(access) > 0 && string(access) != "null" {
		var policy AccessPolicy
		if err = json.Unmarshal(access, &policy); err != nil {
			return Revision{}, false, err
		}
		revision.Access = &policy
	}
	return revision, staged, nil
}

func loadStagedRevision(ctx context.Context, reader sqlReader, item revisionImport, id string) (Revision, []byte, []byte, []byte, error) {
	var revision Revision
	var snapshot, conflicts, access []byte
	err := reader.QueryRowContext(ctx, `SELECT id,workspace_id,epoch,kind,snapshot,conflicts,access,network_head FROM workspace_import_revisions WHERE import_id=? AND id=?`, item.id, id).Scan(&revision.ID, &revision.WorkspaceID, &revision.Epoch, &revision.Kind, &snapshot, &conflicts, &access, &revision.NetworkHead)
	if err != nil {
		return Revision{}, nil, nil, nil, err
	}
	revision.Parents, err = loadStagedParents(ctx, reader, item.id, id)
	return revision, snapshot, conflicts, access, err
}

func loadLiveRevisionMetadata(ctx context.Context, reader sqlReader, workspaceID, id string) (Revision, []byte, []byte, []byte, error) {
	var revision Revision
	var snapshot, conflicts, access []byte
	err := reader.QueryRowContext(ctx, `SELECT id,workspace_id,epoch,kind,snapshot,conflicts,access,network_head FROM revisions WHERE id=? AND workspace_id=?`, id, workspaceID).Scan(&revision.ID, &revision.WorkspaceID, &revision.Epoch, &revision.Kind, &snapshot, &conflicts, &access, &revision.NetworkHead)
	if err != nil {
		return Revision{}, nil, nil, nil, err
	}
	revision.Parents, err = loadParentsFrom(ctx, reader, id)
	return revision, snapshot, conflicts, access, err
}

func loadStagedParents(ctx context.Context, reader sqlReader, importID, revisionID string) ([]string, error) {
	rows, err := reader.QueryContext(ctx, `SELECT parent_id FROM workspace_import_parents WHERE import_id=? AND revision_id=? ORDER BY position`, importID, revisionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var parents []string
	for rows.Next() {
		var parent string
		if err = rows.Scan(&parent); err != nil {
			return nil, err
		}
		parents = append(parents, parent)
	}
	return parents, rows.Err()
}

func loadImportedProofs(ctx context.Context, reader sqlReader, item revisionImport, revisionID string, staged bool) ([]Proof, error) {
	if !staged {
		return loadProofsFrom(ctx, reader, revisionID)
	}
	rows, err := reader.QueryContext(ctx, `SELECT device_id,public_key,signature FROM workspace_import_proofs WHERE import_id=? AND revision_id=? ORDER BY device_id`, item.id, revisionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var proofs []Proof
	for rows.Next() {
		var proof Proof
		if err = rows.Scan(&proof.DeviceID, &proof.PublicKey, &proof.Signature); err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	return proofs, rows.Err()
}

func validateImportedHistory(ctx context.Context, reader sqlReader, item revisionImport, manifest RevisionManifest, revisions map[string]Revision, staged map[string]bool, devices map[string]DeviceRecord, network NetworkBundle) ([]string, error) {
	ordered := sortedManifestRevisions(revisions)
	if err := validateImportedRevisionShapes(manifest, ordered); err != nil {
		return nil, err
	}
	if err := validateCompleteHistory(ordered, revisions); err != nil {
		return nil, err
	}
	if err := validateGenesis(revisions); err != nil {
		return nil, err
	}
	if err := validatePolicyAnchor(revisions); err != nil {
		return nil, err
	}
	heads, err := validateBundleHeads(Bundle{WorkspaceID: manifest.WorkspaceID, Epoch: manifest.Epoch, Heads: manifest.Heads}, revisions)
	if err != nil {
		return nil, err
	}
	if err = authorizeImportedHistory(ctx, reader, item, revisions, staged, devices, network); err != nil {
		return nil, err
	}
	return heads, nil
}

func validateImportedRevisionShapes(manifest RevisionManifest, revisions []Revision) error {
	for _, revision := range revisions {
		if revision.WorkspaceID != manifest.WorkspaceID || revision.Epoch < 1 || revision.Epoch > manifest.Epoch {
			return errors.New("revision import contains another workspace or future epoch")
		}
		if err := validateRevisionShape(revision); err != nil {
			return err
		}
		for _, parent := range revision.Parents {
			if !validRevisionID(parent) {
				return errors.New("revision import contains a malformed parent ID")
			}
		}
	}
	return nil
}

func authorizeImportedHistory(ctx context.Context, reader sqlReader, item revisionImport, revisions map[string]Revision, staged map[string]bool, devices map[string]DeviceRecord, network NetworkBundle) error {
	indegree, children, ready := importedRevisionGraph(revisions)
	validated := make(map[string]Revision, len(revisions))
	currentEpoch := int64(0)
	for _, revision := range revisions {
		currentEpoch = max(currentEpoch, revision.Epoch)
	}
	for len(ready) > 0 {
		current := ready
		ready = nil
		for _, id := range current {
			if err := authorizeImportedRevision(ctx, reader, item, revisions[id], staged[id], validated, devices, network, currentEpoch); err != nil {
				return err
			}
			ready = append(ready, releaseImportedChildren(indegree, children[id])...)
		}
		sort.Strings(ready)
	}
	if len(validated) != len(revisions) {
		return errors.New("workspace revision graph contains a cycle")
	}
	return nil
}

func importedRevisionGraph(revisions map[string]Revision) (map[string]int, map[string][]string, []string) {
	indegree := make(map[string]int, len(revisions))
	children := make(map[string][]string, len(revisions))
	var ready []string
	for id, revision := range revisions {
		indegree[id] = len(revision.Parents)
		if len(revision.Parents) == 0 {
			ready = append(ready, id)
		}
		for _, parent := range revision.Parents {
			children[parent] = append(children[parent], id)
		}
	}
	sort.Strings(ready)
	return indegree, children, ready
}

func authorizeImportedRevision(ctx context.Context, reader sqlReader, item revisionImport, revision Revision, staged bool, validated map[string]Revision, devices map[string]DeviceRecord, network NetworkBundle, currentEpoch int64) error {
	proofs, err := loadImportedProofs(ctx, reader, item, revision.ID, staged)
	if err != nil {
		return err
	}
	revision.Proofs = proofs
	if err = authorizeRevision(revision, validated, devices, network, currentEpoch, !staged); err != nil {
		return fmt.Errorf("authorize revision %s: %w", revision.ID, err)
	}
	revision.Proofs = nil
	validated[revision.ID] = revision
	return nil
}

func releaseImportedChildren(indegree map[string]int, children []string) []string {
	ready := make([]string, 0, len(children))
	for _, child := range children {
		indegree[child]--
		if indegree[child] == 0 {
			ready = append(ready, child)
		}
	}
	return ready
}
