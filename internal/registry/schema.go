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
 conflicts BLOB NOT NULL,
 access BLOB,
 network_head TEXT NOT NULL DEFAULT ''
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
CREATE TABLE IF NOT EXISTS workspace_heads (
 workspace_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 PRIMARY KEY(workspace_id,revision_id)
);
CREATE TABLE IF NOT EXISTS workspace_quarantine (
 workspace_id TEXT NOT NULL,
 source_device_id TEXT NOT NULL,
 head_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 reason TEXT NOT NULL,
 received_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,source_device_id,head_id)
);
CREATE TABLE IF NOT EXISTS workspace_access_conflicts (
 workspace_id TEXT PRIMARY KEY,
 conflict_id TEXT NOT NULL UNIQUE,
 base_revision_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_imports (
 id TEXT PRIMARY KEY,
 peer_id TEXT NOT NULL,
 workspace_id TEXT NOT NULL,
 mode TEXT NOT NULL,
 manifest_hash TEXT NOT NULL,
 workspace_name TEXT NOT NULL,
 root TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 heads BLOB NOT NULL,
 manifest_next TEXT NOT NULL DEFAULT '',
 manifest_finished INTEGER NOT NULL DEFAULT 1,
 expires_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS workspace_import_binding ON workspace_imports(peer_id,workspace_id,mode);
CREATE TABLE IF NOT EXISTS workspace_import_manifest (
 import_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 wire_bytes INTEGER NOT NULL,
 requested INTEGER NOT NULL,
 received INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(import_id,revision_id)
);
CREATE TABLE IF NOT EXISTS workspace_import_revisions (
 import_id TEXT NOT NULL,
 id TEXT NOT NULL,
 workspace_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 kind TEXT NOT NULL,
 snapshot BLOB NOT NULL,
 conflicts BLOB NOT NULL,
 access BLOB,
 network_head TEXT NOT NULL,
 PRIMARY KEY(import_id,id)
);
CREATE TABLE IF NOT EXISTS workspace_import_parents (
 import_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 parent_id TEXT NOT NULL,
 position INTEGER NOT NULL,
 PRIMARY KEY(import_id,revision_id,parent_id)
);
CREATE TABLE IF NOT EXISTS workspace_import_proofs (
 import_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 device_id TEXT NOT NULL,
 public_key BLOB NOT NULL,
 signature BLOB NOT NULL,
 PRIMARY KEY(import_id,revision_id,device_id)
);
CREATE TABLE IF NOT EXISTS networks (
 id TEXT PRIMARY KEY,
 epoch INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS network_events (
 id TEXT PRIMARY KEY,
 network_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 version INTEGER NOT NULL DEFAULT 0,
 parents BLOB NOT NULL DEFAULT 'null',
 selected_parent TEXT NOT NULL DEFAULT '',
 recovery_ids BLOB NOT NULL DEFAULT 'null',
 action TEXT NOT NULL,
 device_id TEXT NOT NULL,
 device_name TEXT NOT NULL,
 device_public_key BLOB NOT NULL,
 role TEXT NOT NULL,
 signer_id TEXT NOT NULL,
 signer_public_key BLOB NOT NULL,
 signature BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS network_conflicts (
 network_id TEXT PRIMARY KEY,
 conflict_id TEXT NOT NULL UNIQUE,
 base_event_id TEXT NOT NULL,
 head_ids BLOB NOT NULL
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
	if err = migrateSchemaColumns(ctx, tx); err != nil {
		return err
	}
	legacy, err := loadLegacyWorkspaces(ctx, tx)
	if err != nil {
		return err
	}
	for _, workspace := range legacy {
		if err = store.migrateLegacyWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
	}
	if err = store.migrateWorkspacePolicies(ctx, tx); err != nil {
		return err
	}
	if err = purgeExpiredImports(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateSchemaColumns(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		table, name, alter string
	}{
		{"revisions", "conflicts", `ALTER TABLE revisions ADD COLUMN conflicts BLOB NOT NULL DEFAULT 'null'`},
		{"revisions", "access", `ALTER TABLE revisions ADD COLUMN access BLOB`},
		{"revisions", "network_head", `ALTER TABLE revisions ADD COLUMN network_head TEXT NOT NULL DEFAULT ''`},
		{"network_events", "version", `ALTER TABLE network_events ADD COLUMN version INTEGER NOT NULL DEFAULT 0`},
		{"network_events", "parents", `ALTER TABLE network_events ADD COLUMN parents BLOB NOT NULL DEFAULT 'null'`},
		{"network_events", "selected_parent", `ALTER TABLE network_events ADD COLUMN selected_parent TEXT NOT NULL DEFAULT ''`},
		{"network_events", "recovery_ids", `ALTER TABLE network_events ADD COLUMN recovery_ids BLOB NOT NULL DEFAULT 'null'`},
		{"workspace_imports", "manifest_next", `ALTER TABLE workspace_imports ADD COLUMN manifest_next TEXT NOT NULL DEFAULT ''`},
		{"workspace_imports", "manifest_finished", `ALTER TABLE workspace_imports ADD COLUMN manifest_finished INTEGER NOT NULL DEFAULT 1`},
	}
	for _, column := range columns {
		if err := ensureTableColumn(ctx, tx, column.table, column.name, column.alter); err != nil {
			return err
		}
	}
	return nil
}

func ensureTableColumn(ctx context.Context, tx *sql.Tx, table, wanted, alter string) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		found = found || name == wanted
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = tx.ExecContext(ctx, alter)
	return err
}

type legacyWorkspace struct {
	name string
	body []byte
}

func loadLegacyWorkspaces(ctx context.Context, tx *sql.Tx) ([]legacyWorkspace, error) {
	rows, err := tx.QueryContext(ctx, `SELECT w.name,w.registry FROM workspaces w LEFT JOIN workspace_protocol p ON p.name=w.name WHERE p.name IS NULL ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var legacy []legacyWorkspace
	for rows.Next() {
		var workspace legacyWorkspace
		if err = rows.Scan(&workspace.name, &workspace.body); err != nil {
			return nil, err
		}
		legacy = append(legacy, workspace)
	}
	return legacy, rows.Err()
}

func (store *Store) migrateLegacyWorkspace(ctx context.Context, tx *sql.Tx, workspace legacyWorkspace) error {
	state, err := config.DecodeStoredWorkspace(workspace.body)
	if err != nil {
		return fmt.Errorf("migrate workspace %q: %w", workspace.name, err)
	}
	snapshotBody, err := encodeSnapshot(state)
	if err != nil {
		return fmt.Errorf("migrate workspace %q: %w", workspace.name, err)
	}
	workspaceID := newWorkspaceID()
	genesis, err := makeRevision(workspaceID, 1, "genesis", nil, snapshotBody, nil, localPolicy(store.identity.ID()), store.identity)
	if err != nil {
		return err
	}
	if err = insertRevision(tx, genesis); err != nil {
		return fmt.Errorf("migrate workspace %q revision: %w", workspace.name, err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,1,?)`, workspace.name, workspaceID, genesis.ID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, workspaceID, genesis.ID)
	}
	return err
}

type policyMigration struct {
	name, workspaceID, head string
	epoch                   int64
}

func (store *Store) migrateWorkspacePolicies(ctx context.Context, tx *sql.Tx) error {
	candidates, err := loadPolicyMigrations(ctx, tx)
	if err != nil {
		return err
	}
	for _, item := range candidates {
		if err = store.migrateWorkspacePolicy(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func loadPolicyMigrations(ctx context.Context, tx *sql.Tx) ([]policyMigration, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,workspace_id,epoch,head_id FROM workspace_protocol ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var candidates []policyMigration
	for rows.Next() {
		var item policyMigration
		if err = rows.Scan(&item.name, &item.workspaceID, &item.epoch, &item.head); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

func (store *Store) migrateWorkspacePolicy(ctx context.Context, tx *sql.Tx, item policyMigration) error {
	var access []byte
	if err := tx.QueryRowContext(ctx, `SELECT access FROM revisions WHERE id=?`, item.head).Scan(&access); err != nil {
		return err
	}
	if len(access) == 0 || string(access) == "null" {
		snapshot, err := loadRevisionSnapshot(tx, item.head)
		if err != nil {
			return err
		}
		anchor, err := makeRevision(item.workspaceID, item.epoch, "access", []string{item.head}, snapshot, nil, localPolicy(store.identity.ID()), store.identity)
		if err != nil {
			return err
		}
		if err = insertRevision(tx, anchor); err != nil {
			return err
		}
		item.head = anchor.ID
		if _, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET head_id=? WHERE name=?`, item.head, item.name); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO workspace_heads(workspace_id,revision_id) VALUES(?,?)`, item.workspaceID, item.head)
	return err
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
