package syncnode

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Database string
	Identity string
}

func DefaultPaths() (Paths, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		state = filepath.Join(home, ".local", "state")
	}
	directory := filepath.Join(state, "ws", "node")
	return Paths{
		Database: filepath.Join(directory, "node.db"),
		Identity: filepath.Join(directory, "identity.key"),
	}, nil
}
