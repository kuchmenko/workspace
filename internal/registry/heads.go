package registry

import (
	"context"
	"database/sql"
	"sort"
)

func (store *Store) loadHeads(ctx context.Context, workspaceID string) ([]string, error) {
	return loadHeadsFrom(ctx, store.db, workspaceID)
}

func loadHeadsFrom(ctx context.Context, reader sqlReader, workspaceID string) ([]string, error) {
	rows, err := reader.QueryContext(ctx, `SELECT revision_id FROM workspace_heads WHERE workspace_id=? ORDER BY revision_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var heads []string
	for rows.Next() {
		var head string
		if err = rows.Scan(&head); err != nil {
			return nil, err
		}
		heads = append(heads, head)
	}
	return heads, rows.Err()
}

func replaceHeads(ctx context.Context, tx *sql.Tx, workspaceID string, heads []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_heads WHERE workspace_id=?`, workspaceID); err != nil {
		return err
	}
	for _, head := range heads {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, workspaceID, head); err != nil {
			return err
		}
	}
	return nil
}

func reduceHeads(tx *sql.Tx, candidates []string) ([]string, error) {
	unique := map[string]bool{}
	for _, candidate := range candidates {
		unique[candidate] = true
	}
	for left := range unique {
		ancestor, err := headIsAncestor(tx, left, unique)
		if err != nil {
			return nil, err
		}
		if ancestor {
			delete(unique, left)
		}
	}
	heads := make([]string, 0, len(unique))
	for head := range unique {
		heads = append(heads, head)
	}
	sort.Strings(heads)
	return heads, nil
}

func headIsAncestor(tx *sql.Tx, candidate string, heads map[string]bool) (bool, error) {
	for other := range heads {
		if candidate == other {
			continue
		}
		ancestor, err := isAncestor(tx, candidate, other)
		if err != nil || ancestor {
			return ancestor, err
		}
	}
	return false, nil
}

func quarantineBundle(ctx context.Context, tx *sql.Tx, bundle Bundle, sourceID, reason string) error {
	for _, head := range bundle.Heads {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO workspace_quarantine(workspace_id,source_device_id,head_id,epoch,reason,received_at) VALUES(?,?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, bundle.WorkspaceID, sourceID, head, bundle.Epoch, reason); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM workspace_quarantine WHERE rowid IN (SELECT rowid FROM workspace_quarantine WHERE workspace_id=? AND source_device_id=? ORDER BY received_at DESC,head_id DESC LIMIT -1 OFFSET 100)`, bundle.WorkspaceID, sourceID)
	return err
}
