package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/kuchmenko/workspace/internal/config"
)

var ErrWorkspaceAccessConflict = errors.New("workspace has an unresolved access conflict")

type WorkspaceAccessConflict struct {
	ID          string                `json:"id"`
	WorkspaceID string                `json:"workspace_id"`
	Base        string                `json:"base"`
	Heads       []WorkspaceAccessHead `json:"heads"`
}

type WorkspaceAccessHead struct {
	ID     string       `json:"id"`
	Epoch  int64        `json:"epoch"`
	Policy AccessPolicy `json:"policy"`
}

type accessDivergence struct {
	base  string
	heads []string
}

func (accessDivergence) Error() string {
	return ErrWorkspaceAccessConflict.Error()
}

func (accessDivergence) Unwrap() error {
	return ErrWorkspaceAccessConflict
}

func (store *Store) AccessConflict(ctx context.Context, name string) (WorkspaceAccessConflict, error) {
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return WorkspaceAccessConflict{}, err
	}
	var conflict WorkspaceAccessConflict
	conflict.WorkspaceID = workspace.WorkspaceID
	err = store.db.QueryRowContext(ctx, `SELECT conflict_id,base_revision_id FROM workspace_access_conflicts WHERE workspace_id=?`, workspace.WorkspaceID).Scan(&conflict.ID, &conflict.Base)
	if err != nil {
		return WorkspaceAccessConflict{}, err
	}
	heads, err := store.loadHeads(ctx, workspace.WorkspaceID)
	if err != nil {
		return WorkspaceAccessConflict{}, err
	}
	for _, head := range heads {
		policy, policyErr := store.policyAt(ctx, head)
		if policyErr != nil {
			return WorkspaceAccessConflict{}, policyErr
		}
		var epoch int64
		if policyErr = store.db.QueryRowContext(ctx, `SELECT epoch FROM revisions WHERE id=?`, head).Scan(&epoch); policyErr != nil {
			return WorkspaceAccessConflict{}, policyErr
		}
		conflict.Heads = append(conflict.Heads, WorkspaceAccessHead{ID: head, Epoch: epoch, Policy: policy})
	}
	return conflict, nil
}

func accessConflictBase(tx *sql.Tx, workspaceID string) (string, bool, error) {
	var base string
	err := tx.QueryRow(`SELECT base_revision_id FROM workspace_access_conflicts WHERE workspace_id=?`, workspaceID).Scan(&base)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return base, err == nil, err
}

func requireNoAccessConflict(tx *sql.Tx, workspaceID string) error {
	_, found, err := accessConflictBase(tx, workspaceID)
	if err != nil {
		return err
	}
	if found {
		return ErrWorkspaceAccessConflict
	}
	return nil
}

func (store *Store) authorizationPolicy(ctx context.Context, workspace Workspace) (AccessPolicy, error) {
	var base string
	err := store.db.QueryRowContext(ctx, `SELECT base_revision_id FROM workspace_access_conflicts WHERE workspace_id=?`, workspace.WorkspaceID).Scan(&base)
	if errors.Is(err, sql.ErrNoRows) {
		base = workspace.Head
	} else if err != nil {
		return AccessPolicy{}, err
	}
	return store.policyAt(ctx, base)
}

func persistAccessDivergence(ctx context.Context, tx *sql.Tx, name, workspaceID string, candidates []string) error {
	heads, err := reduceHeads(tx, candidates)
	if err != nil {
		return err
	}
	base, err := commonAncestorSet(tx, heads)
	if err != nil {
		return err
	}
	conflictID := accessConflictID(workspaceID, base, heads)
	if err = replaceHeads(ctx, tx, workspaceID, heads); err != nil {
		return err
	}
	var epoch int64
	for _, head := range heads {
		var candidate int64
		if err = tx.QueryRow(`SELECT epoch FROM revisions WHERE id=?`, head).Scan(&candidate); err != nil {
			return err
		}
		epoch = max(epoch, candidate)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_access_conflicts(workspace_id,conflict_id,base_revision_id) VALUES(?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET conflict_id=excluded.conflict_id,base_revision_id=excluded.base_revision_id`, workspaceID, conflictID, base); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET epoch=? WHERE name=?`, epoch, name); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return ErrWorkspaceAccessConflict
}

func commonAncestorSet(tx *sql.Tx, heads []string) (string, error) {
	if len(heads) < 2 {
		return "", errors.New("access conflict requires multiple heads")
	}
	base := heads[0]
	for _, head := range heads[1:] {
		var found bool
		var err error
		base, found, err = commonAncestor(tx, base, head)
		if err != nil {
			return "", err
		}
		if !found {
			return "", errors.New("access conflict heads have no common ancestor")
		}
	}
	return base, nil
}

func accessConflictID(workspaceID, base string, heads []string) string {
	body, _ := json.Marshal(struct {
		Domain      string   `json:"domain"`
		WorkspaceID string   `json:"workspace_id"`
		Base        string   `json:"base"`
		Heads       []string `json:"heads"`
	}{Domain: "workspace-access-conflict-v1", WorkspaceID: workspaceID, Base: base, Heads: heads})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func (store *Store) ResolveAccessConflict(ctx context.Context, name, conflictID, policyHead, stateHead string) (Workspace, error) {
	localActive, _, err := store.localNetworkPresence(ctx)
	if err != nil {
		return Workspace{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	resolution, err := store.loadAccessResolution(ctx, tx, name, conflictID, policyHead, stateHead, localActive)
	if err != nil {
		return Workspace{}, err
	}
	revision, err := store.makeAccessResolution(tx, resolution, policyHead, stateHead)
	if err != nil {
		return Workspace{}, err
	}
	if err = persistAccessResolution(ctx, tx, name, resolution, revision); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, name)
}

type accessResolution struct {
	workspaceID, root string
	revisionNumber    int64
	heads             []string
}

func (store *Store) loadAccessResolution(ctx context.Context, tx *sql.Tx, name, conflictID, policyHead, stateHead string, localActive bool) (accessResolution, error) {
	var resolution accessResolution
	if err := tx.QueryRowContext(ctx, `SELECT p.workspace_id,w.root,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=?`, name).Scan(&resolution.workspaceID, &resolution.root, &resolution.revisionNumber); err != nil {
		return resolution, err
	}
	var storedID, base string
	if err := tx.QueryRow(`SELECT conflict_id,base_revision_id FROM workspace_access_conflicts WHERE workspace_id=?`, resolution.workspaceID).Scan(&storedID, &base); err != nil {
		return resolution, err
	}
	if storedID != conflictID {
		return resolution, errors.New("workspace access conflict changed")
	}
	heads, err := loadHeadsFrom(ctx, tx, resolution.workspaceID)
	if err != nil {
		return resolution, err
	}
	if !containsString(heads, policyHead) || !containsString(heads, stateHead) {
		return resolution, errors.New("resolution heads must belong to the current access conflict")
	}
	basePolicy, err := policyAtTx(tx, base)
	if err != nil {
		return resolution, err
	}
	if basePolicy.Role(store.identity.ID(), localActive) != WorkspaceAdmin {
		return resolution, errors.New("local device is not an administrator at the access conflict base")
	}
	resolution.heads = heads
	return resolution, nil
}

func (store *Store) makeAccessResolution(tx *sql.Tx, resolution accessResolution, policyHead, stateHead string) (Revision, error) {
	policy, err := policyAtTx(tx, policyHead)
	if err != nil {
		return Revision{}, err
	}
	snapshot, err := loadRevisionSnapshot(tx, stateHead)
	if err != nil {
		return Revision{}, err
	}
	conflicts, err := loadRevisionConflicts(tx, stateHead)
	if err != nil {
		return Revision{}, err
	}
	var epoch int64
	for _, head := range resolution.heads {
		var candidate int64
		if err = tx.QueryRow(`SELECT epoch FROM revisions WHERE id=?`, head).Scan(&candidate); err != nil {
			return Revision{}, err
		}
		epoch = max(epoch, candidate)
	}
	return makeRevision(resolution.workspaceID, epoch+1, "access-resolution", resolution.heads, snapshot, conflicts, policy, store.identity)
}

func persistAccessResolution(ctx context.Context, tx *sql.Tx, name string, resolution accessResolution, revision Revision) error {
	if err := insertRevision(tx, revision); err != nil {
		return err
	}
	state, err := decodeSnapshot(revision.Snapshot)
	if err != nil {
		return err
	}
	state.RestoreRoot(resolution.root)
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, resolution.revisionNumber)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("workspace changed during access conflict resolution")
	}
	result, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET epoch=?,head_id=? WHERE name=? AND workspace_id=?`, revision.Epoch, revision.ID, name, resolution.workspaceID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("workspace changed during access conflict resolution")
	}
	if err = replaceHeads(ctx, tx, resolution.workspaceID, []string{revision.ID}); err != nil {
		return err
	}
	if err = replaceConflicts(ctx, tx, resolution.workspaceID, revision.ID, revision.Conflicts); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM workspace_access_conflicts WHERE workspace_id=?`, resolution.workspaceID)
	return err
}
