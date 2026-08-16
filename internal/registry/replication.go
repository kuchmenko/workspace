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
	return Bundle{WorkspaceID: workspace.WorkspaceID, Epoch: workspace.Epoch, Heads: []string{workspace.Head}, Revisions: revisions}, nil
}

func (store *Store) Attach(ctx context.Context, name, root string, bundle Bundle) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	head, err := validateBundle(bundle)
	if err != nil {
		return Workspace{}, err
	}
	state, err := stateAt(bundle.Revisions, head)
	if err != nil {
		return Workspace{}, err
	}
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return Workspace{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, revision := range bundle.Revisions {
		if err = insertRevision(tx, revision); err != nil {
			return Workspace{}, err
		}
	}
	if err = validateStoredParents(tx, bundle.WorkspaceID); err != nil {
		return Workspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,1,?)`, name, canonical, body); err != nil {
		return Workspace{}, fmt.Errorf("attach workspace: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_protocol(name,workspace_id,epoch,head_id) VALUES(?,?,?,?)`, name, bundle.WorkspaceID, bundle.Epoch, head); err != nil {
		return Workspace{}, err
	}
	if err = replaceConflicts(ctx, tx, bundle.WorkspaceID, head, revisionConflicts(bundle.Revisions, head)); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, name)
}

func (store *Store) Integrate(ctx context.Context, name string, bundle Bundle) (Workspace, []Conflict, error) {
	remoteHead, err := validateBundle(bundle)
	if err != nil {
		return Workspace{}, nil, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var workspaceID, localHead, root string
	var epoch, revisionNumber int64
	if err = tx.QueryRowContext(ctx, `SELECT p.workspace_id,p.epoch,p.head_id,w.root,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=?`, name).Scan(&workspaceID, &epoch, &localHead, &root, &revisionNumber); err != nil {
		return Workspace{}, nil, err
	}
	if workspaceID != bundle.WorkspaceID {
		return Workspace{}, nil, errors.New("workspace ID does not match")
	}
	if epoch != bundle.Epoch {
		return Workspace{}, nil, errors.New("workspace epoch does not match")
	}
	for _, incoming := range bundle.Revisions {
		if err = insertRevision(tx, incoming); err != nil {
			return Workspace{}, nil, err
		}
	}
	if err = validateStoredParents(tx, workspaceID); err != nil {
		return Workspace{}, nil, err
	}

	head, state, conflicts, err := store.integrateHeads(tx, bundle, workspaceID, epoch, localHead, remoteHead)
	if err != nil {
		return Workspace{}, nil, err
	}
	if state != nil {
		if err = persistIntegration(ctx, tx, state, root, name, workspaceID, localHead, head, revisionNumber, conflicts); err != nil {
			return Workspace{}, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Workspace{}, nil, err
	}
	workspace, err := store.LoadByName(ctx, name)
	return workspace, conflicts, err
}

func persistIntegration(ctx context.Context, tx *sql.Tx, state *config.Workspace, root, name, workspaceID, localHead, head string, revisionNumber int64, conflicts []Conflict) error {
	state.RestoreRoot(root)
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, revisionNumber); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_protocol SET head_id=? WHERE name=? AND head_id=?`, head, name, localHead); err != nil {
		return err
	}
	return replaceConflicts(ctx, tx, workspaceID, head, conflicts)
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
	base, found, err := commonAncestor(tx, localHead, remoteHead)
	if err != nil {
		return "", nil, nil, err
	}
	if !found {
		return "", nil, nil, errors.New("divergent revisions have no common ancestor")
	}
	parents := []string{localHead, remoteHead}
	sort.Strings(parents)
	baseBody, err := loadRevisionSnapshot(tx, base)
	if err != nil {
		return "", nil, nil, err
	}
	leftBody, err := loadRevisionSnapshot(tx, parents[0])
	if err != nil {
		return "", nil, nil, err
	}
	rightBody, err := loadRevisionSnapshot(tx, parents[1])
	if err != nil {
		return "", nil, nil, err
	}
	mergedBody, conflicts, err := mergeSnapshots(baseBody, leftBody, rightBody)
	if err != nil {
		return "", nil, nil, err
	}
	merged, err := makeRevision(workspaceID, epoch, "merge", parents, mergedBody, conflicts, store.identity)
	if err != nil {
		return "", nil, nil, err
	}
	if err = insertRevision(tx, merged); err != nil {
		return "", nil, nil, err
	}
	state, err := decodeSnapshot(mergedBody)
	return merged.ID, state, conflicts, err
}

func (store *Store) loadRevisions(ctx context.Context, workspaceID string) ([]Revision, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,epoch,kind,snapshot,conflicts FROM revisions WHERE workspace_id=? ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	var revisions []Revision
	for rows.Next() {
		var revision Revision
		var conflicts []byte
		revision.WorkspaceID = workspaceID
		if err = rows.Scan(&revision.ID, &revision.Epoch, &revision.Kind, &revision.Snapshot, &conflicts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(conflicts, &revision.Conflicts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
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

func validateBundle(bundle Bundle) (string, error) {
	if bundle.WorkspaceID == "" || bundle.Epoch < 1 {
		return "", errors.New("bundle workspace identity is invalid")
	}
	if len(bundle.Heads) != 1 || bundle.Heads[0] == "" {
		return "", errors.New("bundle must contain exactly one head")
	}
	known := make(map[string]bool, len(bundle.Revisions))
	for _, revision := range bundle.Revisions {
		if revision.WorkspaceID != bundle.WorkspaceID || revision.Epoch != bundle.Epoch {
			return "", errors.New("bundle contains a revision for another workspace or epoch")
		}
		if err := verifyRevision(revision); err != nil {
			return "", err
		}
		known[revision.ID] = true
	}
	if !known[bundle.Heads[0]] {
		return "", errors.New("bundle does not contain its head revision")
	}
	return bundle.Heads[0], nil
}

func stateAt(revisions []Revision, id string) (*config.Workspace, error) {
	for _, revision := range revisions {
		if revision.ID == id {
			return decodeSnapshot(revision.Snapshot)
		}
	}
	return nil, errors.New("revision not found")
}

func revisionConflicts(revisions []Revision, id string) []Conflict {
	for _, revision := range revisions {
		if revision.ID == id {
			return append([]Conflict(nil), revision.Conflicts...)
		}
	}
	return nil
}

func replaceConflicts(ctx context.Context, tx *sql.Tx, workspaceID, revisionID string, conflicts []Conflict) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_conflicts WHERE workspace_id=?`, workspaceID); err != nil {
		return err
	}
	for _, conflict := range conflicts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_conflicts(workspace_id,revision_id,path,base,left_value,right_value) VALUES(?,?,?,?,?,?)`, workspaceID, revisionID, conflict.Path, conflict.Base, conflict.Left, conflict.Right); err != nil {
			return err
		}
	}
	return nil
}

func validateStoredParents(tx *sql.Tx, workspaceID string) error {
	var missing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM revision_parents p JOIN revisions child ON child.id=p.revision_id LEFT JOIN revisions parent ON parent.id=p.parent_id WHERE child.workspace_id=? AND (parent.id IS NULL OR parent.workspace_id<>child.workspace_id)`, workspaceID).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return errors.New("revision history contains a missing or foreign parent")
	}
	return nil
}

func isAncestor(tx *sql.Tx, ancestor, descendant string) (bool, error) {
	if ancestor == descendant {
		return true, nil
	}
	distances, err := ancestorDistances(tx, descendant)
	if err != nil {
		return false, err
	}
	_, found := distances[ancestor]
	return found, nil
}

func commonAncestor(tx *sql.Tx, left, right string) (string, bool, error) {
	leftDistances, err := ancestorDistances(tx, left)
	if err != nil {
		return "", false, err
	}
	rightDistances, err := ancestorDistances(tx, right)
	if err != nil {
		return "", false, err
	}
	best := ""
	bestDistance := int(^uint(0) >> 1)
	for candidate, leftDistance := range leftDistances {
		rightDistance, found := rightDistances[candidate]
		if !found {
			continue
		}
		distance := max(leftDistance, rightDistance)
		if distance < bestDistance || distance == bestDistance && candidate < best {
			best, bestDistance = candidate, distance
		}
	}
	return best, best != "", nil
}

func ancestorDistances(tx *sql.Tx, head string) (map[string]int, error) {
	distances := map[string]int{head: 0}
	queue := []string{head}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		parents, err := revisionParents(tx, current)
		if err != nil {
			return nil, err
		}
		for _, parent := range parents {
			if _, found := distances[parent]; found {
				continue
			}
			distances[parent] = distances[current] + 1
			queue = append(queue, parent)
		}
	}
	return distances, nil
}

func revisionParents(tx *sql.Tx, revisionID string) ([]string, error) {
	rows, err := tx.Query(`SELECT parent_id FROM revision_parents WHERE revision_id=? ORDER BY position`, revisionID)
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

func loadRevisionSnapshot(tx *sql.Tx, id string) ([]byte, error) {
	var body []byte
	err := tx.QueryRow(`SELECT snapshot FROM revisions WHERE id=?`, id).Scan(&body)
	return body, err
}

func loadRevisionState(tx *sql.Tx, id string) (*config.Workspace, error) {
	body, err := loadRevisionSnapshot(tx, id)
	if err != nil {
		return nil, err
	}
	return decodeSnapshot(body)
}
