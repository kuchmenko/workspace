package syncservice

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/kuchmenko/workspace/internal/config"
)

func Canonicalize(data []byte) (*config.Workspace, []byte, string, error) {
	ws, err := config.DecodeCanonicalWorkspace(data)
	if err != nil {
		return nil, nil, "", err
	}
	canonical, err := config.EncodeCanonicalWorkspace(ws)
	if err != nil {
		return nil, nil, "", err
	}
	return ws, canonical, semanticHash(canonical), nil
}

func semanticHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
