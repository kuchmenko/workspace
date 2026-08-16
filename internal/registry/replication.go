package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

func (store *Store) Export(ctx context.Context, name string) (Bundle, error) {
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return Bundle{}, err
	}
	revisions, err := store.loadRevisions(ctx, workspace.WorkspaceID)
	if err != nil {
		return Bundle{}, err
	}
	heads, err := store.loadHeads(ctx, workspace.WorkspaceID)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{WorkspaceID: workspace.WorkspaceID, Epoch: workspace.Epoch, Heads: heads, Revisions: revisions}, nil
}

func (store *Store) Attach(ctx context.Context, name, root string, bundle Bundle) (Workspace, error) {
	return store.attach(ctx, name, root, bundle, "", false)
}

func (store *Store) AttachFrom(ctx context.Context, name, root string, bundle Bundle, sourceID string) (Workspace, error) {
	return store.attach(ctx, name, root, bundle, sourceID, true)
}

func (store *Store) attach(ctx context.Context, name, root string, bundle Bundle, sourceID string, authorize bool) (Workspace, error) {
	prepared, err := store.prepareAttachment(ctx, name, root, bundle, sourceID, authorize)
	if err != nil {
		return Workspace{}, err
	}
	if err = store.persistAttachment(ctx, prepared, bundle); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, prepared.name)
}

type attachment struct {
	name, root, head string
	heads            []string
	body             []byte
}

func (store *Store) prepareAttachment(ctx context.Context, name, root string, bundle Bundle, sourceID string, authorize bool) (attachment, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return attachment{}, errors.New("workspace name is required")
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return attachment{}, err
	}
	heads, err := store.validateBundle(ctx, bundle, authorize)
	if err != nil {
		return attachment{}, err
	}
	if len(heads) != 1 {
		return attachment{}, errors.New("cannot attach a workspace with unresolved divergent heads")
	}
	head := heads[0]
	if authorize && !store.attachmentAuthorized(ctx, bundle, head, sourceID) {
		return attachment{}, errors.New("local device or workspace source is not authorized")
	}
	state, err := stateAt(bundle.Revisions, head)
	if err != nil {
		return attachment{}, err
	}
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return attachment{}, err
	}
	return attachment{name: name, root: canonical, head: head, heads: heads, body: body}, nil
}

func (store *Store) attachmentAuthorized(ctx context.Context, bundle Bundle, head, sourceID string) bool {
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return false
	}
	policy := revisionPolicy(bundle.Revisions, head)
	return policy.Role(store.identity.ID(), active[store.identity.ID()]) != "" && policy.Role(sourceID, active[sourceID]) != ""
}

func (store *Store) persistAttachment(ctx context.Context, prepared attachment, bundle Bundle) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, revision := range bundle.Revisions {
		if err = insertRevision(tx, revision); err != nil {
			return err
		}
	}
	if err = validateStoredParents(tx, bundle.WorkspaceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,1,?)`, prepared.name, prepared.root, prepared.body); err != nil {
		return fmt.Errorf("attach workspace: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,?,?)`, prepared.name, bundle.WorkspaceID, bundle.Epoch, prepared.head); err != nil {
		return err
	}
	if err = replaceHeads(ctx, tx, bundle.WorkspaceID, prepared.heads); err != nil {
		return err
	}
	if err = replaceConflicts(ctx, tx, bundle.WorkspaceID, prepared.head, revisionConflicts(bundle.Revisions, prepared.head)); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) Integrate(ctx context.Context, name string, bundle Bundle) (Workspace, []Conflict, error) {
	return store.integrate(ctx, name, bundle, "", false)
}

func (store *Store) IntegrateFrom(ctx context.Context, name string, bundle Bundle, sourceID string) (Workspace, []Conflict, error) {
	return store.integrate(ctx, name, bundle, sourceID, true)
}

func (store *Store) integrate(ctx context.Context, name string, bundle Bundle, sourceID string, authorize bool) (Workspace, []Conflict, error) {
	remoteHeads, err := store.validateBundle(ctx, bundle, authorize)
	if err != nil {
		return Workspace{}, nil, err
	}
	active, err := store.integrationDevices(ctx, sourceID, authorize)
	if err != nil {
		return Workspace{}, nil, err
	}
	conflicts, err := store.persistIncoming(ctx, name, bundle, sourceID, authorize, active, remoteHeads)
	if err != nil {
		return Workspace{}, nil, err
	}
	workspace, err := store.LoadByName(ctx, name)
	return workspace, conflicts, err
}

func (store *Store) integrationDevices(ctx context.Context, sourceID string, authorize bool) (map[string]bool, error) {
	if !authorize {
		return map[string]bool{}, nil
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return nil, err
	}
	if sourceID == "" || !active[sourceID] {
		return nil, errors.New("workspace source is not an active network device")
	}
	return active, nil
}

func (store *Store) persistIncoming(ctx context.Context, name string, bundle Bundle, sourceID string, authorize bool, active map[string]bool, remoteHeads []string) ([]Conflict, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	base, policy, err := prepareIncoming(ctx, tx, name, bundle, sourceID, authorize, active)
	if err != nil {
		return nil, err
	}
	role := integrationRole(policy, store.identity.ID(), authorize, active)
	head, state, conflicts, heads, err := store.integrateHeadSet(tx, bundle, base.workspaceID, bundle.Epoch, base.head, remoteHeads, role)
	if err != nil {
		return nil, err
	}
	if err = persistIncomingState(ctx, tx, state, base, name, head, bundle.Epoch, conflicts); err != nil {
		return nil, err
	}
	if err = commitIncoming(ctx, tx, base.workspaceID, heads); err != nil {
		return nil, err
	}
	return conflicts, nil
}

func prepareIncoming(ctx context.Context, tx *sql.Tx, name string, bundle Bundle, sourceID string, authorize bool, active map[string]bool) (incomingBase, AccessPolicy, error) {
	base, err := loadIncomingBase(ctx, tx, name)
	if err != nil {
		return base, AccessPolicy{}, err
	}
	if base.workspaceID != bundle.WorkspaceID {
		return base, AccessPolicy{}, errors.New("workspace ID does not match")
	}
	policy, err := policyAtTx(tx, base.head)
	if err != nil {
		return base, policy, err
	}
	if err = acceptBundleEpoch(ctx, tx, bundle, sourceID, authorize, base.epoch); err != nil {
		return base, policy, err
	}
	if err = requireAuthorizedSource(policy, sourceID, authorize, active); err != nil {
		return base, policy, err
	}
	err = insertIncomingRevisions(tx, base.workspaceID, bundle.Revisions)
	return base, policy, err
}

func requireAuthorizedSource(policy AccessPolicy, sourceID string, authorize bool, active map[string]bool) error {
	if authorize && policy.Role(sourceID, active[sourceID]) == "" {
		return errors.New("workspace source is not authorized")
	}
	return nil
}

func persistIncomingState(ctx context.Context, tx *sql.Tx, state *config.Workspace, base incomingBase, name, head string, epoch int64, conflicts []Conflict) error {
	if state == nil {
		return nil
	}
	return persistIntegration(ctx, tx, state, base.root, name, base.workspaceID, base.head, head, epoch, base.revision, conflicts)
}

func commitIncoming(ctx context.Context, tx *sql.Tx, workspaceID string, heads []string) error {
	if err := replaceHeads(ctx, tx, workspaceID, heads); err != nil {
		return err
	}
	return tx.Commit()
}

type incomingBase struct {
	workspaceID string
	head        string
	root        string
	epoch       int64
	revision    int64
}

func loadIncomingBase(ctx context.Context, tx *sql.Tx, name string) (incomingBase, error) {
	var base incomingBase
	err := tx.QueryRowContext(ctx, `SELECT p.workspace_id,p.epoch,p.head_id,w.root,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=?`, name).Scan(&base.workspaceID, &base.epoch, &base.head, &base.root, &base.revision)
	return base, err
}

func acceptBundleEpoch(ctx context.Context, tx *sql.Tx, bundle Bundle, sourceID string, authorize bool, localEpoch int64) error {
	if bundle.Epoch >= localEpoch {
		return nil
	}
	if !authorize {
		return errors.New("workspace epoch is stale")
	}
	if err := quarantineBundle(ctx, tx, bundle, sourceID, "workspace epoch is stale"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return errors.New("workspace epoch is stale")
}

func integrationRole(policy AccessPolicy, localID string, authorize bool, active map[string]bool) string {
	if authorize {
		return policy.Role(localID, active[localID])
	}
	return policy.Role(localID, true)
}

func insertIncomingRevisions(tx *sql.Tx, workspaceID string, revisions []Revision) error {
	for _, incoming := range revisions {
		if err := insertRevision(tx, incoming); err != nil {
			return err
		}
	}
	return validateStoredParents(tx, workspaceID)
}

func persistIntegration(ctx context.Context, tx *sql.Tx, state *config.Workspace, root, name, workspaceID, localHead, head string, epoch, revisionNumber int64, conflicts []Conflict) error {
	state.RestoreRoot(root)
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, revisionNumber); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET epoch=?,head_id=? WHERE name=? AND head_id=?`, epoch, head, name, localHead); err != nil {
		return err
	}
	return replaceConflicts(ctx, tx, workspaceID, head, conflicts)
}

func (store *Store) integrateHeadSet(tx *sql.Tx, bundle Bundle, workspaceID string, epoch int64, localHead string, remoteHeads []string, role string) (string, *config.Workspace, []Conflict, []string, error) {
	current := localHead
	var state *config.Workspace
	var conflicts []Conflict
	for _, remoteHead := range remoteHeads {
		if role == WorkspaceReplica {
			continue
		}
		next, nextState, nextConflicts, err := store.integrateHeads(tx, bundle, workspaceID, epoch, current, remoteHead)
		if err != nil {
			return "", nil, nil, nil, err
		}
		current = next
		if nextState != nil {
			state, conflicts = nextState, nextConflicts
		}
	}
	if role != WorkspaceReplica {
		return current, state, conflicts, []string{current}, nil
	}
	heads, err := reduceHeads(tx, append(remoteHeads, localHead))
	if err != nil {
		return "", nil, nil, nil, err
	}
	if len(heads) == 1 && heads[0] != localHead {
		state, err = loadRevisionState(tx, heads[0])
		if err != nil {
			return "", nil, nil, nil, err
		}
		return heads[0], state, revisionConflicts(bundle.Revisions, heads[0]), heads, nil
	}
	return localHead, nil, nil, heads, nil
}

func (store *Store) integrateHeads(tx *sql.Tx, bundle Bundle, workspaceID string, epoch int64, localHead, remoteHead string) (string, *config.Workspace, []Conflict, error) {
	remoteIsAncestor, err := isAncestor(tx, remoteHead, localHead)
	if err != nil || localHead == remoteHead || remoteIsAncestor {
		return localHead, nil, nil, err
	}
	localIsAncestor, err := isAncestor(tx, localHead, remoteHead)
	if err != nil {
		return "", nil, nil, err
	}
	if localIsAncestor {
		state, err := loadRevisionState(tx, remoteHead)
		return remoteHead, state, revisionConflicts(bundle.Revisions, remoteHead), err
	}
	return store.mergeHeads(tx, workspaceID, epoch, localHead, remoteHead)
}

func (store *Store) mergeHeads(tx *sql.Tx, workspaceID string, epoch int64, localHead, remoteHead string) (string, *config.Workspace, []Conflict, error) {
	leftPolicy, err := policyAtTx(tx, localHead)
	if err != nil {
		return "", nil, nil, err
	}
	rightPolicy, err := policyAtTx(tx, remoteHead)
	if err != nil {
		return "", nil, nil, err
	}
	if !equalPolicy(leftPolicy, rightPolicy) {
		return "", nil, nil, errors.New("divergent workspace access policies require admin resolution")
	}
	base, found, err := commonAncestor(tx, localHead, remoteHead)
	if err != nil {
		return "", nil, nil, err
	}
	if !found {
		return "", nil, nil, errors.New("divergent revisions have no common ancestor")
	}
	parents := []string{localHead, remoteHead}
	sort.Strings(parents)
	baseBody, leftBody, rightBody, err := loadMergeSnapshots(tx, base, parents)
	if err != nil {
		return "", nil, nil, err
	}
	mergedBody, conflicts, err := mergeSnapshots(baseBody, leftBody, rightBody)
	if err != nil {
		return "", nil, nil, err
	}
	merged, err := makeRevision(workspaceID, epoch, "merge", parents, mergedBody, conflicts, leftPolicy, store.identity)
	if err != nil {
		return "", nil, nil, err
	}
	if err = insertRevision(tx, merged); err != nil {
		return "", nil, nil, err
	}
	state, err := decodeSnapshot(mergedBody)
	return merged.ID, state, conflicts, err
}

func loadMergeSnapshots(tx *sql.Tx, base string, parents []string) ([]byte, []byte, []byte, error) {
	baseBody, err := loadRevisionSnapshot(tx, base)
	if err != nil {
		return nil, nil, nil, err
	}
	leftBody, err := loadRevisionSnapshot(tx, parents[0])
	if err != nil {
		return nil, nil, nil, err
	}
	rightBody, err := loadRevisionSnapshot(tx, parents[1])
	return baseBody, leftBody, rightBody, err
}

func (store *Store) loadRevisions(ctx context.Context, workspaceID string) ([]Revision, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,epoch,kind,snapshot,conflicts,access FROM revisions WHERE workspace_id=? ORDER BY id`, workspaceID)
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
		revisions[index].Parents, err = store.loadParents(ctx, revisions[index].ID)
		if err != nil {
			return nil, err
		}
		revisions[index].Proofs, err = store.loadProofs(ctx, revisions[index].ID)
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
		if err := rows.Scan(&revision.ID, &revision.Epoch, &revision.Kind, &revision.Snapshot, &conflicts, &access); err != nil {
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

func (store *Store) loadParents(ctx context.Context, revisionID string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT parent_id FROM revision_parents WHERE revision_id=? ORDER BY position`, revisionID)
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

func (store *Store) loadProofs(ctx context.Context, revisionID string) ([]Proof, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT device_id,public_key,signature FROM revision_proofs WHERE revision_id=? ORDER BY device_id`, revisionID)
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
