package syncclient

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Directory   string
	Credentials string
	Database    string
	Attempt     string
}

func DefaultPaths() (Paths, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		base = filepath.Join(home, ".local", "state")
	}
	directory := filepath.Join(base, "ws", "sync")
	return Paths{Directory: directory, Credentials: filepath.Join(directory, "credentials.json"), Database: filepath.Join(directory, "client.db"), Attempt: filepath.Join(directory, "pairing-attempt.json")}, nil
}
