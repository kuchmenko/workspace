package syncprotocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const ProtocolVersion uint64 = 1

const (
	revisionIDDomain        = "ws/revision/v1\x00"
	revisionSignatureDomain = "ws/revision-signature/v1\x00"
)

type RevisionKind uint64

const (
	RevisionGenesis RevisionKind = iota + 1
	RevisionWrite
	RevisionResolution
	RevisionAuthority
	RevisionReconcile
	RevisionCheckpoint
)

type RevisionID [sha256.Size]byte
type WorkspaceID [sha256.Size]byte
type RecoveryEpoch [sha256.Size]byte
type NodeID [sha256.Size]byte

type RevisionCore struct {
	ProtocolVersion        uint64        `cbor:"1,keyasint"`
	WorkspaceID            WorkspaceID   `cbor:"2,keyasint"`
	RecoveryEpoch          RecoveryEpoch `cbor:"3,keyasint"`
	Kind                   RevisionKind  `cbor:"4,keyasint"`
	Parents                []RevisionID  `cbor:"5,keyasint"`
	Generation             uint64        `cbor:"6,keyasint"`
	SnapshotSchema         uint64        `cbor:"7,keyasint"`
	Snapshot               []byte        `cbor:"8,keyasint"`
	Conflicts              []byte        `cbor:"9,keyasint"`
	Author                 NodeID        `cbor:"10,keyasint"`
	AuthorSequence         uint64        `cbor:"11,keyasint"`
	PreviousAuthorRevision RevisionID    `cbor:"12,keyasint"`
}

type SignatureProof struct {
	NodeID    NodeID
	Signature []byte
}

func (core *RevisionCore) normalize() {
	if core.Parents == nil {
		core.Parents = []RevisionID{}
	}
	if core.Snapshot == nil {
		core.Snapshot = []byte{}
	}
	if core.Conflicts == nil {
		core.Conflicts = []byte{}
	}
}

func (core RevisionCore) Validate() error {
	if core.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", core.ProtocolVersion)
	}
	if core.WorkspaceID == (WorkspaceID{}) {
		return errors.New("workspace ID is required")
	}
	if core.RecoveryEpoch == (RecoveryEpoch{}) {
		return errors.New("recovery epoch is required")
	}
	if core.SnapshotSchema == 0 {
		return errors.New("snapshot schema is required")
	}
	if len(core.Snapshot) == 0 {
		return errors.New("snapshot is required")
	}
	if len(core.Conflicts) == 0 {
		return errors.New("conflict state is required")
	}
	if !sortedUniqueRevisionIDs(core.Parents) {
		return errors.New("revision parents must be sorted and unique")
	}
	return core.validateKind()
}

func (core RevisionCore) validateKind() error {
	authored := core.Author != (NodeID{}) || core.AuthorSequence != 0 || core.PreviousAuthorRevision != (RevisionID{})
	switch core.Kind {
	case RevisionGenesis:
		if len(core.Parents) != 0 || core.Generation != 0 || authored {
			return errors.New("genesis must have no parents, generation, or author")
		}
	case RevisionWrite:
		if len(core.Parents) != 1 {
			return errors.New("write must have exactly one parent")
		}
		if err := core.validateAuthor(); err != nil {
			return err
		}
	case RevisionResolution:
		if len(core.Parents) == 0 {
			return errors.New("resolution must have at least one parent")
		}
		if err := core.validateAuthor(); err != nil {
			return err
		}
	case RevisionAuthority, RevisionCheckpoint:
		if len(core.Parents) == 0 || core.Generation == 0 || authored {
			return errors.New("authority and checkpoint revisions require parents and detached admin proofs")
		}
	case RevisionReconcile:
		if len(core.Parents) < 2 || core.Generation == 0 || authored {
			return errors.New("reconcile must have at least two parents and no author")
		}
	default:
		return fmt.Errorf("unsupported revision kind %d", core.Kind)
	}
	if core.Kind != RevisionGenesis && core.Generation == 0 {
		return errors.New("non-genesis revision generation is required")
	}
	return nil
}

func (core RevisionCore) validateAuthor() error {
	if core.Author == (NodeID{}) || core.AuthorSequence == 0 {
		return errors.New("authored revision requires author and sequence")
	}
	if core.AuthorSequence == 1 && core.PreviousAuthorRevision != (RevisionID{}) {
		return errors.New("first authored revision cannot have a previous authored revision")
	}
	if core.AuthorSequence > 1 && core.PreviousAuthorRevision == (RevisionID{}) {
		return errors.New("authored revision sequence requires previous authored revision")
	}
	return nil
}

func RevisionIDFor(core RevisionCore) (RevisionID, error) {
	canonical, err := EncodeRevisionCore(core)
	if err != nil {
		return RevisionID{}, err
	}
	payload := make([]byte, 0, len(revisionIDDomain)+len(canonical))
	payload = append(payload, revisionIDDomain...)
	payload = append(payload, canonical...)
	return sha256.Sum256(payload), nil
}

func NodeIDFor(publicKey ed25519.PublicKey) (NodeID, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return NodeID{}, errors.New("invalid Ed25519 public key")
	}
	return sha256.Sum256(publicKey), nil
}

func SignRevision(privateKey ed25519.PrivateKey, revisionID RevisionID) (SignatureProof, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return SignatureProof{}, errors.New("invalid Ed25519 private key")
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	nodeID, err := NodeIDFor(publicKey)
	if err != nil {
		return SignatureProof{}, err
	}
	message := signatureMessage(revisionID)
	return SignatureProof{NodeID: nodeID, Signature: ed25519.Sign(privateKey, message)}, nil
}

func VerifyRevisionProof(publicKey ed25519.PublicKey, revisionID RevisionID, proof SignatureProof) bool {
	nodeID, err := NodeIDFor(publicKey)
	if err != nil || nodeID != proof.NodeID || len(proof.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(publicKey, signatureMessage(revisionID), proof.Signature)
}

func signatureMessage(revisionID RevisionID) []byte {
	message := make([]byte, 0, len(revisionSignatureDomain)+len(revisionID))
	message = append(message, revisionSignatureDomain...)
	message = append(message, revisionID[:]...)
	return message
}

func sortedUniqueRevisionIDs(ids []RevisionID) bool {
	for index := 1; index < len(ids); index++ {
		if bytes.Compare(ids[index-1][:], ids[index][:]) >= 0 {
			return false
		}
	}
	return true
}

func (id RevisionID) String() string { return hex.EncodeToString(id[:]) }
func (id NodeID) String() string     { return hex.EncodeToString(id[:]) }
