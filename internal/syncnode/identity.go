package syncnode

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/syncprotocol"
)

type Identity struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	nodeID     syncprotocol.NodeID
}

func OpenOrCreateIdentity(path string) (Identity, error) {
	seed, err := os.ReadFile(path)
	if err == nil {
		return identityFromSeed(seed)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	seed = make([]byte, ed25519.SeedSize)
	if _, err = rand.Read(seed); err != nil {
		return Identity{}, err
	}
	if err = createPrivateFile(path, seed); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Identity{}, err
		}
		seed, err = os.ReadFile(path)
		if err != nil {
			return Identity{}, err
		}
	}
	return identityFromSeed(seed)
}

func CreateRecoveryKey(path string) (ed25519.PublicKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := createPrivateFile(path, seed); err != nil {
		return nil, err
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	return append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...), nil
}

func LoadRecoveryKey(path string) (ed25519.PublicKey, error) {
	seed, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	identity, err := identityFromSeed(seed)
	if err != nil {
		return nil, err
	}
	return identity.PublicKey(), nil
}

func identityFromSeed(seed []byte) (Identity, error) {
	if len(seed) != ed25519.SeedSize {
		return Identity{}, fmt.Errorf("identity seed must be %d bytes", ed25519.SeedSize)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	nodeID, err := syncprotocol.NodeIDFor(publicKey)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		privateKey: append(ed25519.PrivateKey(nil), privateKey...),
		publicKey:  append(ed25519.PublicKey(nil), publicKey...),
		nodeID:     nodeID,
	}, nil
}

func (identity Identity) NodeID() syncprotocol.NodeID { return identity.nodeID }

func (identity Identity) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), identity.publicKey...)
}

func (identity Identity) Sign(revisionID syncprotocol.RevisionID) (syncprotocol.SignatureProof, error) {
	return syncprotocol.SignRevision(identity.privateKey, revisionID)
}

func createPrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".identity-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(temporary, path); err != nil {
		return err
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryFile.Close()
	return directoryFile.Sync()
}
