package registry

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/kuchmenko/workspace/internal/device"
)

const protocolVersion = 1

type Revision struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspace_id"`
	Epoch       int64         `json:"epoch"`
	Kind        string        `json:"kind"`
	Parents     []string      `json:"parents"`
	Snapshot    []byte        `json:"snapshot"`
	Conflicts   []Conflict    `json:"conflicts,omitempty"`
	Access      *AccessPolicy `json:"access,omitempty"`
	Proofs      []Proof       `json:"proofs"`
}

type Proof struct {
	DeviceID  string `json:"device_id"`
	PublicKey []byte `json:"public_key"`
	Signature []byte `json:"signature"`
}

type Bundle struct {
	WorkspaceID string     `json:"workspace_id"`
	Epoch       int64      `json:"epoch"`
	Heads       []string   `json:"heads"`
	Revisions   []Revision `json:"revisions"`
}

type revisionCore struct {
	Protocol    int           `json:"protocol"`
	WorkspaceID string        `json:"workspace_id"`
	Epoch       int64         `json:"epoch"`
	Kind        string        `json:"kind"`
	Parents     []string      `json:"parents"`
	Snapshot    []byte        `json:"snapshot"`
	Conflicts   []Conflict    `json:"conflicts,omitempty"`
	Access      *AccessPolicy `json:"access,omitempty"`
}

func newWorkspaceID() string {
	return uuid.NewString()
}

func makeRevision(workspaceID string, epoch int64, kind string, parents []string, snapshot []byte, conflicts []Conflict, access AccessPolicy, author device.Identity) (Revision, error) {
	parents = append([]string(nil), parents...)
	sort.Strings(parents)
	access, err := normalizePolicy(access)
	if err != nil {
		return Revision{}, err
	}
	core := revisionCore{Protocol: protocolVersion, WorkspaceID: workspaceID, Epoch: epoch, Kind: kind, Parents: parents, Snapshot: snapshot, Conflicts: conflicts, Access: &access}
	body, err := json.Marshal(core)
	if err != nil {
		return Revision{}, err
	}
	digest := sha256.Sum256(body)
	id := hex.EncodeToString(digest[:])
	return Revision{
		ID:          id,
		WorkspaceID: workspaceID,
		Epoch:       epoch,
		Kind:        kind,
		Parents:     parents,
		Snapshot:    append([]byte(nil), snapshot...),
		Conflicts:   append([]Conflict(nil), conflicts...),
		Access:      &access,
		Proofs: []Proof{{
			DeviceID:  author.ID(),
			PublicKey: author.PublicKey(),
			Signature: author.Sign(digest[:]),
		}},
	}, nil
}

func verifyRevision(revision Revision) error {
	core := revisionCore{
		Protocol:    protocolVersion,
		WorkspaceID: revision.WorkspaceID,
		Epoch:       revision.Epoch,
		Kind:        revision.Kind,
		Parents:     append([]string(nil), revision.Parents...),
		Snapshot:    revision.Snapshot,
		Conflicts:   revision.Conflicts,
		Access:      revision.Access,
	}
	sort.Strings(core.Parents)
	if !equalStrings(core.Parents, revision.Parents) {
		return errors.New("revision parents are not sorted")
	}
	body, err := json.Marshal(core)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if revision.ID != hex.EncodeToString(digest[:]) {
		return errors.New("revision content ID does not match body")
	}
	if _, err = decodeSnapshot(revision.Snapshot); err != nil {
		return fmt.Errorf("invalid revision snapshot: %w", err)
	}
	if len(revision.Proofs) == 0 {
		return errors.New("revision has no proof")
	}
	for _, proof := range revision.Proofs {
		if len(proof.PublicKey) != ed25519.PublicKeySize || proof.DeviceID != device.IDForPublicKey(proof.PublicKey) {
			return errors.New("revision proof identity is invalid")
		}
		if !ed25519.Verify(proof.PublicKey, digest[:], proof.Signature) {
			return errors.New("revision proof signature is invalid")
		}
	}
	return nil
}

func insertRevision(tx *sql.Tx, revision Revision) error {
	if err := verifyRevision(revision); err != nil {
		return err
	}
	conflicts, err := json.Marshal(revision.Conflicts)
	if err != nil {
		return err
	}
	access, err := json.Marshal(revision.Access)
	if err != nil {
		return err
	}
	if err = persistRevision(tx, revision, conflicts, access); err != nil {
		return err
	}
	return persistRevisionRelations(tx, revision)
}

func persistRevision(tx *sql.Tx, revision Revision, conflicts, access []byte) error {
	result, err := tx.Exec(`INSERT OR IGNORE INTO revisions(id,workspace_id,epoch,kind,snapshot,conflicts,access) VALUES(?,?,?,?,?,?,?)`, revision.ID, revision.WorkspaceID, revision.Epoch, revision.Kind, revision.Snapshot, conflicts, access)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return verifyStoredRevision(tx, revision, conflicts, access)
	}
	return nil
}

func persistRevisionRelations(tx *sql.Tx, revision Revision) error {
	for position, parent := range revision.Parents {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO revision_parents(revision_id,parent_id,position) VALUES(?,?,?)`, revision.ID, parent, position); err != nil {
			return err
		}
	}
	for _, proof := range revision.Proofs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO revision_proofs(revision_id,device_id,public_key,signature) VALUES(?,?,?,?)`, revision.ID, proof.DeviceID, proof.PublicKey, proof.Signature); err != nil {
			return err
		}
	}
	return nil
}

func verifyStoredRevision(tx *sql.Tx, revision Revision, conflicts, access []byte) error {
	var workspaceID, kind string
	var epoch int64
	var snapshot, storedConflicts, storedAccess []byte
	if err := tx.QueryRow(`SELECT workspace_id,epoch,kind,snapshot,conflicts,access FROM revisions WHERE id=?`, revision.ID).Scan(&workspaceID, &epoch, &kind, &snapshot, &storedConflicts, &storedAccess); err != nil {
		return err
	}
	if storedAccess == nil {
		storedAccess = []byte("null")
	}
	if workspaceID != revision.WorkspaceID || epoch != revision.Epoch || kind != revision.Kind || string(snapshot) != string(revision.Snapshot) || string(storedConflicts) != string(conflicts) || string(storedAccess) != string(access) {
		return errors.New("revision ID collision")
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
