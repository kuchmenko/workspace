package registry

import (
	"context"
	"database/sql"
	"encoding/json"
)

func loadRevisionsFrom(ctx context.Context, reader sqlReader, workspaceID string) ([]Revision, error) {
	rows, err := reader.QueryContext(ctx, `SELECT id,epoch,kind,snapshot,conflicts,access,network_head FROM revisions WHERE workspace_id=? ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	revisions, err := scanRevisions(rows, workspaceID)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	for index := range revisions {
		revisions[index].Parents, err = loadParentsFrom(ctx, reader, revisions[index].ID)
		if err != nil {
			return nil, err
		}
		revisions[index].Proofs, err = loadProofsFrom(ctx, reader, revisions[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return revisions, nil
}

func scanRevisions(rows *sql.Rows, workspaceID string) ([]Revision, error) {
	var revisions []Revision
	for rows.Next() {
		var revision Revision
		var conflicts, access []byte
		revision.WorkspaceID = workspaceID
		if err := rows.Scan(&revision.ID, &revision.Epoch, &revision.Kind, &revision.Snapshot, &conflicts, &access, &revision.NetworkHead); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(conflicts, &revision.Conflicts); err != nil {
			return nil, err
		}
		if len(access) > 0 && string(access) != "null" {
			var policy AccessPolicy
			if err := json.Unmarshal(access, &policy); err != nil {
				return nil, err
			}
			revision.Access = &policy
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func loadParentsFrom(ctx context.Context, reader sqlReader, revisionID string) ([]string, error) {
	rows, err := reader.QueryContext(ctx, `SELECT parent_id FROM revision_parents WHERE revision_id=? ORDER BY position`, revisionID)
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

func loadProofsFrom(ctx context.Context, reader sqlReader, revisionID string) ([]Proof, error) {
	rows, err := reader.QueryContext(ctx, `SELECT device_id,public_key,signature FROM revision_proofs WHERE revision_id=? ORDER BY device_id`, revisionID)
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
