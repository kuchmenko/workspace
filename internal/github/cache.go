package github

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type cacheFile struct {
	Version  int       `json:"version"`
	StoredAt time.Time `json:"stored_at"`
	Repos    []Repo    `json:"repos"`
}

const (
	cacheVersion = 1

	cacheTTL = time.Hour
)

func CacheTTL() time.Duration { return cacheTTL }

func cachePath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "ws", "github-cache.json"), nil
}

func LoadCache() ([]Repo, time.Duration, error) {
	p, err := cachePath()
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	cf, ok := parseCacheFile(data)
	if !ok {
		return nil, 0, nil
	}
	return cf.Repos, time.Since(cf.StoredAt), nil
}

func parseCacheFile(data []byte) (cacheFile, bool) {
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return cacheFile{}, false
	}
	if cf.Version != cacheVersion {
		return cacheFile{}, false
	}
	if !cacheReposLookSane(cf.Repos) {
		return cacheFile{}, false
	}
	return cf, true
}

func cacheReposLookSane(repos []Repo) bool {
	for _, r := range repos {
		if r.Owner == "" && r.SSHURL == "" {
			return false
		}
	}
	return true
}

func SaveCache(repos []Repo) error {
	if len(repos) == 0 {
		return nil
	}
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{
		Version:  cacheVersion,
		StoredAt: time.Now().UTC(),
		Repos:    repos,
	})
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func PurgeCache() error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func CacheFresh() (bool, time.Duration) {
	_, age, err := LoadCache()
	if err != nil {
		return false, 0
	}
	return age > 0 && age < cacheTTL, age
}
