package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

func (store *Store) Conflicts(ctx context.Context, name string) ([]Conflict, error) {
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT path,base,left_value,right_value FROM workspace_conflicts WHERE workspace_id=? ORDER BY path`, workspace.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var conflicts []Conflict
	for rows.Next() {
		var conflict Conflict
		if err = rows.Scan(&conflict.Path, &conflict.Base, &conflict.Left, &conflict.Right); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func (store *Store) Resolve(ctx context.Context, name, path string, value json.RawMessage) (Workspace, error) {
	localActive, _ := store.localNetworkPresence(ctx)
	if err := store.persistResolution(ctx, name, path, value, localActive); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, name)
}

type resolution struct {
	workspaceID, head, root string
	epoch, revisionNumber   int64
	policy                  AccessPolicy
	state                   *config.Workspace
	revision                Revision
	remaining               []Conflict
}

func (store *Store) persistResolution(ctx context.Context, name, path string, value json.RawMessage, localActive bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	prepared, err := store.prepareResolution(ctx, tx, name, path, value, localActive)
	if err != nil {
		return err
	}
	if err = insertRevision(tx, prepared.revision); err != nil {
		return err
	}
	prepared.state.RestoreRoot(prepared.root)
	body, err := config.EncodeWorkspace(prepared.state)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, prepared.revisionNumber); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET head_id=? WHERE name=? AND head_id=?`, prepared.revision.ID, name, prepared.head); err != nil {
		return err
	}
	if err = replaceHeads(ctx, tx, prepared.workspaceID, []string{prepared.revision.ID}); err != nil {
		return err
	}
	if err = replaceConflicts(ctx, tx, prepared.workspaceID, prepared.revision.ID, prepared.remaining); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) prepareResolution(ctx context.Context, tx *sql.Tx, name, path string, value json.RawMessage, localActive bool) (resolution, error) {
	prepared, err := loadResolution(ctx, tx, name)
	if err != nil {
		return prepared, err
	}
	if err = requireNoAccessConflict(tx, prepared.workspaceID); err != nil {
		return prepared, err
	}
	if err = requireConflict(ctx, tx, prepared.workspaceID, prepared.head, path); err != nil {
		return prepared, err
	}
	prepared.policy, err = policyAtTx(tx, prepared.head)
	if err != nil {
		return prepared, err
	}
	if err = requireConflictWriter(prepared.policy, store.identity.ID(), localActive); err != nil {
		return prepared, err
	}
	snapshot, err := loadRevisionSnapshot(tx, prepared.head)
	if err != nil {
		return prepared, err
	}
	resolved, err := resolveSnapshotValue(snapshot, path, value)
	if err != nil {
		return prepared, err
	}
	if err = validateSharedUpdate(prepared.policy, store.identity.ID(), resolved); err != nil {
		return prepared, err
	}
	prepared.state, err = decodeSnapshot(resolved)
	if err != nil {
		return prepared, err
	}
	prepared.remaining, err = unresolvedConflicts(ctx, tx, prepared.workspaceID, prepared.head, path)
	if err != nil {
		return prepared, err
	}
	prepared.revision, err = makeRevision(prepared.workspaceID, prepared.epoch, "resolution", []string{prepared.head}, resolved, prepared.remaining, prepared.policy, store.identity)
	if err != nil {
		return prepared, err
	}
	return prepared, nil
}

func requireConflictWriter(policy AccessPolicy, localID string, localActive bool) error {
	role := policy.Role(localID, localActive)
	if role != WorkspaceAdmin && role != WorkspaceWriter {
		return errors.New("local device cannot resolve workspace conflicts")
	}
	return nil
}

func loadResolution(ctx context.Context, tx *sql.Tx, name string) (resolution, error) {
	var prepared resolution
	err := tx.QueryRowContext(ctx, `SELECT p.workspace_id,p.epoch,p.head_id,w.root,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=?`, name).Scan(&prepared.workspaceID, &prepared.epoch, &prepared.head, &prepared.root, &prepared.revisionNumber)
	return prepared, err
}

func requireConflict(ctx context.Context, tx *sql.Tx, workspaceID, head, path string) error {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM workspace_conflicts WHERE workspace_id=? AND revision_id=? AND path=?`, workspaceID, head, path).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("workspace conflict %q not found", path)
	}
	return err
}

func unresolvedConflicts(ctx context.Context, tx *sql.Tx, workspaceID, revisionID, resolvedPath string) ([]Conflict, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path,base,left_value,right_value FROM workspace_conflicts WHERE workspace_id=? AND revision_id=? AND path<>? ORDER BY path`, workspaceID, revisionID, resolvedPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var conflicts []Conflict
	for rows.Next() {
		var conflict Conflict
		if err = rows.Scan(&conflict.Path, &conflict.Base, &conflict.Left, &conflict.Right); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func resolveSnapshotValue(snapshot []byte, path string, raw json.RawMessage) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(snapshot, &root); err != nil {
		return nil, err
	}
	parts, err := pointerParts(path)
	if err != nil || len(parts) == 0 {
		return nil, errors.New("conflict path is invalid")
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			return nil, errors.New("conflict path does not select an object")
		}
		current = next
	}
	key := parts[len(parts)-1]
	if len(raw) == 0 {
		delete(current, key)
	} else {
		var value any
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("invalid resolution JSON: %w", err)
		}
		current[key] = value
	}
	return json.Marshal(root)
}

func pointerParts(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("JSON pointer must start with slash")
	}
	encoded := strings.Split(strings.TrimPrefix(path, "/"), "/")
	parts := make([]string, len(encoded))
	for index, part := range encoded {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		parts[index] = part
	}
	return parts, nil
}
