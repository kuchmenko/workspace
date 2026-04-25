package github

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// cacheFile is the on-disk envelope for a github-suggestion cache.
// We embed StoredAt + a schema Version so future changes to the Repo
// shape can be detected and the cache invalidated automatically
// without crashing on JSON-unmarshal mismatches.
type cacheFile struct {
	Version  int       `json:"version"`
	StoredAt time.Time `json:"stored_at"`
	Repos    []Repo    `json:"repos"`
}

const (
	// cacheVersion bumps any time the on-disk Repo schema changes in
	// a way that older versions cannot decode. Reads from a different
	// version are treated as a cache miss.
	cacheVersion = 1

	// cacheTTL caps how long a cache is considered fresh enough to
	// serve in lieu of a live fetch. One hour is a good balance: long
	// enough to make repeated `ws add` invocations feel instant, short
	// enough that newly-created GitHub repos surface within a typical
	// workday.
	cacheTTL = time.Hour
)

// CacheTTL returns the freshness window. Exported for tests and for
// callers that want to surface "cached, X minutes old" diagnostics.
func CacheTTL() time.Duration { return cacheTTL }

// cachePath resolves the cache file location, honoring XDG state.
// Single file per user, not per workspace — the cached data is the
// user's own GitHub repos and doesn't depend on which workspace is
// active.
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

// LoadCache reads the cached repo list. Returns (nil, 0, nil) when no
// cache exists (the common cold-start path) — not treated as an error.
// The returned age lets callers decide whether to use the cache,
// refresh it in the background, or force a fresh fetch.
//
// Schema-version mismatches and JSON parse errors are treated as a
// cache miss so a malformed file never blocks the user.
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
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		// Corrupt cache → treat as miss, don't surface to caller.
		return nil, 0, nil
	}
	if cf.Version != cacheVersion {
		return nil, 0, nil
	}
	return cf.Repos, time.Since(cf.StoredAt), nil
}

// SaveCache atomically writes the repo list to the cache file. Writes
// are best-effort: a failed cache save never blocks the live result
// from reaching the caller, so this returns its error but most
// callers should ignore it.
//
// Atomic via tmp + rename so a crash mid-write leaves the previous
// cache intact rather than producing a half-written file.
func SaveCache(repos []Repo) error {
	if len(repos) == 0 {
		// Don't persist empty caches — they'd just produce false
		// "fresh" hits that hide the user's actual repos.
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

// PurgeCache removes the cache file. Called by `ws auth login` or
// other commands that change the GitHub identity, so the next `ws add`
// fetches fresh data scoped to the new account. No-op if the file is
// already gone.
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

// CacheFresh reports whether the on-disk cache is younger than CacheTTL.
// Convenience for callers that don't need the actual repo list — useful
// in diagnostics ("github: cached, 12m old").
func CacheFresh() (bool, time.Duration) {
	_, age, err := LoadCache()
	if err != nil {
		return false, 0
	}
	return age > 0 && age < cacheTTL, age
}
