package repo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectLockExcludesConcurrentMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project")
	first, err := AcquireProjectLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	if _, err = AcquireProjectLock(path); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("contended lock error = %v", err)
	}
	if err = first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireProjectLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestProjectLockUsesPhysicalPathBeforeProjectExists(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	if err := os.Mkdir(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(physical, linked); err != nil {
		t.Fatal(err)
	}

	first, err := AcquireProjectLock(filepath.Join(linked, "project"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()
	if err = os.Mkdir(filepath.Join(physical, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = AcquireProjectLock(filepath.Join(physical, "project")); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("physical path lock error = %v", err)
	}
}
