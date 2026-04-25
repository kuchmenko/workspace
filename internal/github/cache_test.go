package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundtrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	repos := []Repo{
		{Name: "alpha", FullName: "me/alpha", Owner: "me"},
		{Name: "beta", FullName: "me/beta", Owner: "me"},
	}
	if err := SaveCache(repos); err != nil {
		t.Fatal(err)
	}

	got, age, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
	if got[0].Name != "alpha" {
		t.Errorf("got[0] = %q", got[0].Name)
	}
	// Just-saved cache should be fresh.
	if age <= 0 || age > 5*time.Second {
		t.Errorf("age = %v, want a few seconds at most", age)
	}
}

func TestLoadCache_NoFile_NoError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	got, age, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil repos, got %d entries", len(got))
	}
	if age != 0 {
		t.Errorf("age = %v, want 0", age)
	}
}

func TestLoadCache_CorruptIsTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "github-cache.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, age, err := LoadCache()
	if err != nil {
		t.Errorf("corrupt cache should not surface as error, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil from corrupt file, got %d entries", len(got))
	}
	if age != 0 {
		t.Errorf("age should be 0 on corrupt cache, got %v", age)
	}
}

func TestLoadCache_OldVersionIsMiss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hand-written cache with a different Version.
	payload := map[string]interface{}{
		"version":   999,
		"stored_at": time.Now(),
		"repos":     []Repo{{Name: "x"}},
	}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(wsDir, "github-cache.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, _, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("version mismatch should be a miss, got %d entries", len(got))
	}
}

func TestSaveCache_EmptyIsNoop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Empty save must not produce a cache file (otherwise CacheFresh
	// would lie about having data).
	if err := SaveCache(nil); err != nil {
		t.Fatal(err)
	}
	got, _, _ := LoadCache()
	if got != nil {
		t.Error("empty SaveCache should not persist anything")
	}
}

func TestPurgeCache_RemovesFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := SaveCache([]Repo{{Name: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := PurgeCache(); err != nil {
		t.Fatal(err)
	}
	got, _, _ := LoadCache()
	if got != nil {
		t.Errorf("after purge, expected nil, got %d entries", len(got))
	}
}

func TestPurgeCache_NoFile_NoError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := PurgeCache(); err != nil {
		t.Errorf("purge of non-existent cache should be silent, got %v", err)
	}
}

func TestCacheFresh_ReportsFreshAndAge(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := SaveCache([]Repo{{Name: "x"}}); err != nil {
		t.Fatal(err)
	}
	fresh, age := CacheFresh()
	if !fresh {
		t.Error("freshly-saved cache should report fresh=true")
	}
	if age <= 0 {
		t.Errorf("age should be positive, got %v", age)
	}
}

func TestCacheFresh_ReportsStaleWhenNoFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	fresh, _ := CacheFresh()
	if fresh {
		t.Error("no cache should report fresh=false")
	}
}

func TestCacheTTL_IsPositive(t *testing.T) {
	if CacheTTL() <= 0 {
		t.Errorf("CacheTTL = %v, want positive", CacheTTL())
	}
}
