package device

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Identity struct {
	id      string
	private ed25519.PrivateKey
}

func Load(path string) (Identity, error) {
	private, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, private, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return Identity{}, err
		}
		if err = writePrivateKey(path, private); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return Identity{}, err
			}
			private, err = os.ReadFile(path)
			if err != nil {
				return Identity{}, err
			}
		}
	} else if err != nil {
		return Identity{}, err
	}
	if len(private) != ed25519.PrivateKeySize {
		return Identity{}, fmt.Errorf("invalid device identity length %d", len(private))
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return Identity{}, err
	}
	public := ed25519.PrivateKey(private).Public().(ed25519.PublicKey)
	digest := sha256.Sum256(public)
	return Identity{id: hex.EncodeToString(digest[:]), private: private}, nil
}

func DefaultPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "ws", "identity.key"), nil
}

func (identity Identity) ID() string {
	return identity.id
}

func (identity Identity) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), identity.private.Public().(ed25519.PublicKey)...)
}

func (identity Identity) Sign(message []byte) []byte {
	return ed25519.Sign(identity.private, message)
}

func (identity Identity) Signer() crypto.Signer {
	return identity.private
}

func writePrivateKey(path string, private []byte) error {
	directoryPath := filepath.Dir(path)
	file, err := os.CreateTemp(directoryPath, ".identity-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err = file.Write(private); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Link(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(directoryPath)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func IDForPublicKey(public ed25519.PublicKey) string {
	digest := sha256.Sum256(public)
	return hex.EncodeToString(digest[:])
}
