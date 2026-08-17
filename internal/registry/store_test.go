package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestStorePersistsWorkspaceMutations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	database := filepath.Join(t.TempDir(), "registry.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{
		Meta:     config.Meta{Version: 1},
		Projects: map[string]config.Project{},
	}
	created, err := store.Create(context.Background(), "personal", root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Root != root {
		t.Fatalf("created = %#v", created)
	}

	created.State.Projects["workspace"] = config.Project{Path: "personal/workspace"}
	updated, err := store.Update(context.Background(), created.Name, created.Revision, created.State)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision = %d, want 2", updated.Revision)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.LoadByName(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.State.Projects["workspace"]; !ok {
		t.Fatalf("projects = %#v", loaded.State.Projects)
	}
	listed, err := reopened.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "personal" {
		t.Fatalf("listed = %#v", listed)
	}
}

func TestOpenRejectsMissingIdentityForInitializedRegistry(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "registry.db")
	identity := filepath.Join(directory, "identity.key")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnsureNetwork(context.Background(), "arch"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(identity); err != nil {
		t.Fatal(err)
	}
	if reopened, openErr := Open(database); openErr == nil {
		_ = reopened.Close()
		t.Fatal("initialized registry opened with a replacement identity")
	}
	if _, err = os.Stat(identity); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement identity stat error = %v", err)
	}
}

func TestStoreRejectsDuplicateAndStaleWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}}
	created, err := store.Create(ctx, "personal", firstRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Create(ctx, "personal", secondRoot, state); err == nil {
		t.Fatal("duplicate name succeeded")
	}
	if _, err = store.Create(ctx, "other", firstRoot, state); err == nil {
		t.Fatal("duplicate root succeeded")
	}
	if _, err = store.Update(ctx, created.Name, created.Revision, state); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(ctx, created.Name, created.Revision, state); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale update error = %v", err)
	}
}

func TestStoreSetRootChangesOnlyLocalRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	created, err := store.Create(ctx, "personal", oldRoot, &config.Workspace{
		Meta:    config.Meta{Version: 1},
		Aliases: map[string]string{"ws": "personal/workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var revisionsBefore int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions`).Scan(&revisionsBefore); err != nil {
		t.Fatal(err)
	}

	updated, err := store.SetRoot(ctx, created.Name, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Root != newRoot || updated.Revision != created.Revision || updated.WorkspaceID != created.WorkspaceID || updated.Epoch != created.Epoch || updated.Head != created.Head {
		t.Fatalf("updated workspace = %#v, created = %#v", updated, created)
	}
	if updated.State.Aliases["ws"] != "personal/workspace" {
		t.Fatalf("aliases = %#v", updated.State.Aliases)
	}
	var revisionsAfter int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions`).Scan(&revisionsAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore {
		t.Fatalf("revision count = %d, want %d", revisionsAfter, revisionsBefore)
	}
	if _, err = store.LoadByRoot(ctx, oldRoot); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("old root lookup error = %v", err)
	}
	found, err := store.LoadByRoot(ctx, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != created.Name {
		t.Fatalf("workspace at new root = %q", found.Name)
	}
}

func TestStoreSetRootRejectsOwnedRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}}
	if _, err = store.Create(ctx, "first", firstRoot, state); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Create(ctx, "second", secondRoot, state); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetRoot(ctx, "second", firstRoot); err == nil {
		t.Fatal("setting an owned root succeeded")
	}
	second, err := store.LoadByName(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Root != secondRoot {
		t.Fatalf("second root = %q, want %q", second.Root, secondRoot)
	}
}

func TestRegistryFindUsesCanonicalPathBoundariesAndDeepestRoot(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	nested := filepath.Join(root, "private")
	sibling := filepath.Join(parent, "workspace")
	for _, directory := range []string{root, nested, sibling} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	ctx := context.Background()
	for name, directory := range map[string]string{"work": root, "private": nested, "sibling": sibling} {
		if _, err = registry.Create(ctx, name, directory, &config.Workspace{Meta: config.Meta{Version: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	found, err := registry.Find(ctx, filepath.Join(nested, "project"))
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != "private" {
		t.Fatalf("found %q, want private", found.Name)
	}
	if _, err = registry.Find(ctx, filepath.Join(parent, "work-copy")); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("boundary lookup error = %v", err)
	}
}

func TestStoreRestrictsDatabasePermissions(t *testing.T) {
	t.Parallel()

	database := filepath.Join(t.TempDir(), "registry.db")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(database)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("permissions = %o, want 600", permissions)
	}
}
