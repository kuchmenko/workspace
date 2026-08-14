package syncprotocol

import (
	"crypto/ed25519"
	"encoding/hex"
	"reflect"
	"testing"
)

func TestGenesisRevisionVector(t *testing.T) {
	core := revisionVector()
	encoded, err := EncodeRevisionCore(core)
	if err != nil {
		t.Fatal(err)
	}
	revisionID, err := RevisionIDFor(core)
	if err != nil {
		t.Fatal(err)
	}
	const expectedCBOR = "ac01010258200102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20035820fffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedecebeae9e8e7e6e5e4e3e2e1e004010580060007010841a00941800a582000000000000000000000000000000000000000000000000000000000000000000b000c58200000000000000000000000000000000000000000000000000000000000000000"
	const expectedRevisionID = "a4807449c7a3ba5718a8f2c1cd30906e449911b79731c1239d963ba72abc19b8"
	if hex.EncodeToString(encoded) != expectedCBOR {
		t.Fatalf("CBOR mismatch\nwant %s\n got %x", expectedCBOR, encoded)
	}
	if revisionID.String() != expectedRevisionID {
		t.Fatalf("revision ID mismatch\nwant %s\n got %s", expectedRevisionID, revisionID)
	}
	decoded, err := DecodeRevisionCore(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, core) {
		t.Fatalf("round trip mismatch\nwant %#v\n got %#v", core, decoded)
	}
}

func TestRevisionSignatureVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	revisionID, err := RevisionIDFor(revisionVector())
	if err != nil {
		t.Fatal(err)
	}
	proof, err := SignRevision(privateKey, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	const expectedNodeID = "56475aa75463474c0285df5dbf2bcab73da651358839e9b77481b2eab107708c"
	const expectedSignature = "b2510d49fcb7defb1b517f92d4013097d048a90da5ce5ef74c4cdc806a35620e2f237dc9c81cb15b98bec24777631d91fa652aca3f6b84192374db6b86bf760e"
	if proof.NodeID.String() != expectedNodeID {
		t.Fatalf("node ID mismatch\nwant %s\n got %s", expectedNodeID, proof.NodeID)
	}
	if hex.EncodeToString(proof.Signature) != expectedSignature {
		t.Fatalf("signature mismatch\nwant %s\n got %x", expectedSignature, proof.Signature)
	}
	if !VerifyRevisionProof(privateKey.Public().(ed25519.PublicKey), revisionID, proof) {
		t.Fatal("valid proof was rejected")
	}
	proof.Signature[0] ^= 0xff
	if VerifyRevisionProof(privateKey.Public().(ed25519.PublicKey), revisionID, proof) {
		t.Fatal("modified proof was accepted")
	}
}

func TestDecodeRevisionCoreRejectsNonCanonicalEncoding(t *testing.T) {
	core := revisionVector()
	encoded, err := EncodeRevisionCore(core)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = 0xb8
	encoded = append(encoded[:1], append([]byte{0x0c}, encoded[1:]...)...)
	if _, err = DecodeRevisionCore(encoded); err == nil {
		t.Fatal("non-canonical map length was accepted")
	}
}

func TestDecodeRevisionCoreRejectsUnknownAndDuplicateFields(t *testing.T) {
	encoded, err := EncodeRevisionCore(revisionVector())
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), encoded...)
	unknown[0] = 0xad
	unknown = append(unknown, 0x0d, 0x00)
	if _, err = DecodeRevisionCore(unknown); err == nil {
		t.Fatal("unknown field was accepted")
	}
	duplicate := append([]byte(nil), encoded...)
	duplicate[0] = 0xad
	duplicate = append(duplicate, 0x01, 0x01)
	if _, err = DecodeRevisionCore(duplicate); err == nil {
		t.Fatal("duplicate field was accepted")
	}
}

func TestRevisionParentsMustBeSortedAndUnique(t *testing.T) {
	core := revisionVector()
	core.Kind = RevisionReconcile
	core.Generation = 2
	core.Parents = []RevisionID{{2}, {1}}
	if _, err := EncodeRevisionCore(core); err == nil {
		t.Fatal("unsorted parents were accepted")
	}
	core.Parents = []RevisionID{{1}, {1}}
	if _, err := EncodeRevisionCore(core); err == nil {
		t.Fatal("duplicate parents were accepted")
	}
}

func TestAuthoredRevisionRequiresSequenceChain(t *testing.T) {
	core := revisionVector()
	core.Kind = RevisionWrite
	core.Generation = 1
	core.Parents = []RevisionID{{1}}
	core.Author = NodeID{1}
	if _, err := EncodeRevisionCore(core); err == nil {
		t.Fatal("author without sequence was accepted")
	}
	core.AuthorSequence = 2
	if _, err := EncodeRevisionCore(core); err == nil {
		t.Fatal("later author sequence without previous revision was accepted")
	}
	core.PreviousAuthorRevision = RevisionID{2}
	if _, err := EncodeRevisionCore(core); err != nil {
		t.Fatalf("valid author sequence chain was rejected: %v", err)
	}
}

func revisionVector() RevisionCore {
	var workspaceID WorkspaceID
	var epoch RecoveryEpoch
	for index := range workspaceID {
		workspaceID[index] = byte(index + 1)
		epoch[index] = byte(255 - index)
	}
	return RevisionCore{
		ProtocolVersion: ProtocolVersion,
		WorkspaceID:     workspaceID,
		RecoveryEpoch:   epoch,
		Kind:            RevisionGenesis,
		Parents:         []RevisionID{},
		SnapshotSchema:  1,
		Snapshot:        []byte{0xa0},
		Conflicts:       []byte{0x80},
	}
}
