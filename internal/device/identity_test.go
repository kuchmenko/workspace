package device

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIdentityPersistsWithRestrictedPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() {
		t.Fatalf("identity changed: %s != %s", first.ID(), second.ID())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %o", info.Mode().Perm())
	}
}

func TestConcurrentIdentityCreationConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	start := make(chan struct{})
	identities := make(chan Identity, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			identity, err := Load(path)
			identities <- identity
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(identities)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for identity := range identities {
		if id == "" {
			id = identity.ID()
		} else if identity.ID() != id {
			t.Fatalf("concurrent identity IDs differ: %s != %s", identity.ID(), id)
		}
	}
}
