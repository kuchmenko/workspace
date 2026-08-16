package registry

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kuchmenko/workspace/internal/config"
)

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
