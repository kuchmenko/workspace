package registry

import (
	"context"
	"fmt"
)

func (store *Store) OriginBaselines(ctx context.Context, workspaceID string) (map[string]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT project,remote FROM local_project_origins WHERE workspace_id=? ORDER BY project`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	baselines := make(map[string]string)
	for rows.Next() {
		var project, remote string
		if err = rows.Scan(&project, &remote); err != nil {
			return nil, err
		}
		baselines[project] = remote
	}
	return baselines, rows.Err()
}

func (store *Store) SaveOriginBaselines(ctx context.Context, workspaceID string, baselines map[string]string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_project_origins WHERE workspace_id=?`, workspaceID); err != nil {
		return err
	}
	for project, remote := range baselines {
		if project == "" || remote == "" {
			return fmt.Errorf("origin baseline requires project and remote")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO local_project_origins(workspace_id,project,remote) VALUES(?,?,?) ON CONFLICT(workspace_id,project) DO UPDATE SET remote=excluded.remote`, workspaceID, project, remote); err != nil {
			return err
		}
	}
	return tx.Commit()
}
