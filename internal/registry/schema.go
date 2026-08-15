package registry

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kuchmenko/workspace/internal/config"
)

const schema = `
CREATE TABLE IF NOT EXISTS workspaces (
 name TEXT PRIMARY KEY,
 root TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL,
 registry BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_protocol (
 name TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL UNIQUE,
 epoch INTEGER NOT NULL,
 head_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS revisions (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 kind TEXT NOT NULL,
 snapshot BLOB NOT NULL,
 conflicts BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS revisions_workspace ON revisions(workspace_id);
CREATE TABLE IF NOT EXISTS revision_parents (
 revision_id TEXT NOT NULL,
 parent_id TEXT NOT NULL,
 position INTEGER NOT NULL,
 PRIMARY KEY(revision_id,parent_id)
);
CREATE INDEX IF NOT EXISTS revision_parents_parent ON revision_parents(parent_id);
CREATE TABLE IF NOT EXISTS revision_proofs (
 revision_id TEXT NOT NULL,
 device_id TEXT NOT NULL,
 public_key BLOB NOT NULL,
 signature BLOB NOT NULL,
 PRIMARY KEY(revision_id,device_id)
);
CREATE TABLE IF NOT EXISTS workspace_conflicts (
 workspace_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 path TEXT NOT NULL,
 base BLOB,
 left_value BLOB,
 right_value BLOB,
 PRIMARY KEY(workspace_id,revision_id,path)
);
CREATE TABLE IF NOT EXISTS networks (
 id TEXT PRIMARY KEY,
 epoch INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS network_events (
 id TEXT PRIMARY KEY,
 network_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 action TEXT NOT NULL,
 device_id TEXT NOT NULL,
 device_name TEXT NOT NULL,
 device_public_key BLOB NOT NULL,
 role TEXT NOT NULL,
 signer_id TEXT NOT NULL,
 signer_public_key BLOB NOT NULL,
 signature BLOB NOT NULL
);`

func (store *Store) initialize(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	type legacyWorkspace struct {
		name string
		body []byte
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.name,w.registry FROM workspaces w LEFT JOIN workspace_protocol p ON p.name=w.name WHERE p.name IS NULL ORDER BY w.name`)
	if err != nil {
		return err
	}
	var legacy []legacyWorkspace
	for rows.Next() {
		var workspace legacyWorkspace
		if err = rows.Scan(&workspace.name, &workspace.body); err != nil {
			_ = rows.Close()
			return err
		}
		legacy = append(legacy, workspace)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, workspace := range legacy {
		state, decodeErr := config.DecodeStoredWorkspace(workspace.body)
		if decodeErr != nil {
			return fmt.Errorf("migrate workspace %q: %w", workspace.name, decodeErr)
		}
		snapshotBody, encodeErr := encodeSnapshot(state)
		if encodeErr != nil {
			return fmt.Errorf("migrate workspace %q: %w", workspace.name, encodeErr)
		}
		workspaceID := newWorkspaceID()
		genesis, revisionErr := makeRevision(workspaceID, 1, "genesis", nil, snapshotBody, nil, store.identity)
		if revisionErr != nil {
			return revisionErr
		}
		if err = insertRevision(tx, genesis); err != nil {
			return fmt.Errorf("migrate workspace %q revision: %w", workspace.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,1,?)`, workspace.name, workspaceID, genesis.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func workspaceExists(tx *sql.Tx, name string) (bool, error) {
	var found int
	err := tx.QueryRow(`SELECT 1 FROM workspaces WHERE name=?`, name).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}
