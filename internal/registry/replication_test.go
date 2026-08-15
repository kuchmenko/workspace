package registry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
	_ "modernc.org/sqlite"
)

func TestRevisionIDDoesNotDependOnAuthor(t *testing.T) {
	t.Parallel()
	left, err := device.Load(filepath.Join(t.TempDir(), "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := device.Load(filepath.Join(t.TempDir(), "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeSnapshot(testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	leftRevision, err := makeRevision("workspace", 1, "merge", []string{"a", "b"}, body, nil, left)
	if err != nil {
		t.Fatal(err)
	}
	rightRevision, err := makeRevision("workspace", 1, "merge", []string{"b", "a"}, body, nil, right)
	if err != nil {
		t.Fatal(err)
	}
	if leftRevision.ID != rightRevision.ID {
		t.Fatalf("revision IDs differ by author: %s != %s", leftRevision.ID, rightRevision.ID)
	}
	if leftRevision.ID != "97e8d28b4c2f557d3db69aeb99fe427dbae4ff4348f91d4e9188f002e80923e4" {
		t.Fatalf("revision vector = %s", leftRevision.ID)
	}
	if reflect.DeepEqual(leftRevision.Proofs, rightRevision.Proofs) {
		t.Fatal("detached author proofs are equal")
	}
}

func TestMergeCombinesIndependentChangesAndPreservesConflict(t *testing.T) {
	t.Parallel()
	base := testWorkspace()
	base.Aliases["editor"] = "vim"
	base.Projects["workspace"] = config.Project{
		Remote:   "git@example.com:workspace.git",
		Path:     "personal/workspace",
		Status:   config.StatusActive,
		Category: config.CategoryPersonal,
		Branches: []config.BranchMeta{{Name: "main", Machines: []string{"arch"}}},
	}
	left := cloneState(t, base)
	right := cloneState(t, base)
	left.Groups["personal"] = config.Group{Description: "Personal"}
	right.Aliases["shell"] = "zsh"
	leftProject := left.Projects["workspace"]
	leftProject.Branches[0].Machines = append(leftProject.Branches[0].Machines, "asahi")
	leftProject.Branches[0].LastActiveMachine = "asahi"
	leftProject.Branches[0].LastActiveAt = "2026-08-15T10:00:00Z"
	leftProject.Branches[0].CreatedBy = "asahi"
	leftProject.Branches[0].CreatedAt = "2026-08-15T09:00:00Z"
	left.Projects["workspace"] = leftProject
	rightProject := right.Projects["workspace"]
	rightProject.Branches[0].Machines = append(rightProject.Branches[0].Machines, "lxc")
	rightProject.Branches[0].LastActiveMachine = "lxc"
	rightProject.Branches[0].LastActiveAt = "2026-08-15T11:00:00Z"
	rightProject.Branches[0].CreatedBy = "lxc"
	rightProject.Branches[0].CreatedAt = "2026-08-15T08:00:00Z"
	right.Projects["workspace"] = rightProject

	merged, conflicts, err := Merge(base, left, right)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("independent conflicts = %#v", conflicts)
	}
	if merged.Groups["personal"].Description != "Personal" || merged.Aliases["shell"] != "zsh" {
		t.Fatalf("merged independent state = %#v", merged)
	}
	machines := merged.Projects["workspace"].Branches[0].Machines
	if !reflect.DeepEqual(machines, []string{"arch", "asahi", "lxc"}) {
		t.Fatalf("merged machines = %v", machines)
	}
	branch := merged.Projects["workspace"].Branches[0]
	if branch.LastActiveMachine != "lxc" || branch.CreatedBy != "lxc" {
		t.Fatalf("merged observations = %#v", branch)
	}

	left.Aliases["editor"] = "helix"
	right.Aliases["editor"] = "nano"
	merged, conflicts, err = Merge(base, left, right)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Path != "/aliases/editor" {
		t.Fatalf("conflicts = %#v", conflicts)
	}
	if merged.Aliases["editor"] != "vim" {
		t.Fatalf("conflicted value = %q, want base vim", merged.Aliases["editor"])
	}
}

func TestStoresConvergeAcrossFastForwardDivergenceAndConflict(t *testing.T) {
	ctx := context.Background()
	left := openTestStore(t)
	right := openTestStore(t)
	leftRoot := t.TempDir()
	rightRoot := t.TempDir()
	created, err := left.Create(ctx, "shared", leftRoot, testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(initial.Revisions)
	attached, err := right.Attach(ctx, "shared", rightRoot, initial)
	if err != nil {
		t.Fatal(err)
	}
	if attached.WorkspaceID != created.WorkspaceID || attached.Head != created.Head {
		t.Fatalf("attached = %#v, created = %#v", attached, created)
	}

	updateAlias(t, left, leftRoot, "arch", "yes")
	leftBundle, err := left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, conflicts, integrateErr := right.Integrate(ctx, "shared", leftBundle); integrateErr != nil || len(conflicts) != 0 {
		t.Fatalf("fast-forward conflicts=%v error=%v", conflicts, integrateErr)
	}
	updateAlias(t, right, rightRoot, "asahi", "yes")
	rightBundle, err := right.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, conflicts, integrateErr := left.Integrate(ctx, "shared", rightBundle); integrateErr != nil || len(conflicts) != 0 {
		t.Fatalf("reverse fast-forward conflicts=%v error=%v", conflicts, integrateErr)
	}

	updateAlias(t, left, leftRoot, "left-only", "one")
	updateAlias(t, right, rightRoot, "right-only", "two")
	rightBundle, err = right.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	leftMerged, conflicts, err := left.Integrate(ctx, "shared", rightBundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 || leftMerged.State.Aliases["left-only"] != "one" || leftMerged.State.Aliases["right-only"] != "two" {
		t.Fatalf("independent merge workspace=%#v conflicts=%#v", leftMerged, conflicts)
	}
	leftBundle, err = left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	rightMerged, conflicts, err := right.Integrate(ctx, "shared", leftBundle)
	if err != nil {
		t.Fatal(err)
	}
	if rightMerged.Head != leftMerged.Head || len(conflicts) != 0 {
		t.Fatalf("converged heads %s != %s, conflicts=%#v", rightMerged.Head, leftMerged.Head, conflicts)
	}

	updateAlias(t, left, leftRoot, "editor", "helix")
	updateAlias(t, right, rightRoot, "editor", "nano")
	rightBundle, err = right.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	leftConflicted, conflicts, err := left.Integrate(ctx, "shared", rightBundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Path != "/aliases/editor" || leftConflicted.State.Aliases["editor"] != "vim" {
		t.Fatalf("conflict workspace=%#v conflicts=%#v", leftConflicted, conflicts)
	}
	leftBundle, err = left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	rightConflicted, conflicts, err := right.Integrate(ctx, "shared", leftBundle)
	if err != nil {
		t.Fatal(err)
	}
	if rightConflicted.Head != leftConflicted.Head || len(conflicts) != 1 || conflicts[0].Path != "/aliases/editor" {
		t.Fatalf("replicated conflict workspace=%#v conflicts=%#v", rightConflicted, conflicts)
	}
}

func TestStoreMigratesExistingRegistryToSignedGenesis(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "registry.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`CREATE TABLE workspaces(name TEXT PRIMARY KEY,root TEXT NOT NULL UNIQUE,revision INTEGER NOT NULL,registry BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	body, err := config.EncodeWorkspace(testWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,7,?)`, "shared", root, body); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspace, err := store.LoadByName(context.Background(), "shared")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Revision != 7 || workspace.WorkspaceID == "" || workspace.Head == "" || workspace.Epoch != 1 {
		t.Fatalf("migrated workspace = %#v", workspace)
	}
	bundle, err := store.Export(context.Background(), "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Revisions) != 1 || bundle.Revisions[0].Kind != "genesis" || len(bundle.Revisions[0].Proofs) != 1 {
		t.Fatalf("migrated bundle = %#v", bundle)
	}
	identityInfo, err := os.Stat(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if identityInfo.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %o", identityInfo.Mode().Perm())
	}
}

func TestIntegrateRejectsTamperedRevision(t *testing.T) {
	ctx := context.Background()
	left := openTestStore(t)
	right := openTestStore(t)
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	if _, err := left.Create(ctx, "shared", leftRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	bundle, err := left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.Attach(ctx, "shared", rightRoot, bundle); err != nil {
		t.Fatal(err)
	}
	updateAlias(t, left, leftRoot, "tamper", "original")
	bundle, err = left.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	for index := range bundle.Revisions {
		if bundle.Revisions[index].ID == bundle.Heads[0] {
			bundle.Revisions[index].Snapshot[0] ^= 1
		}
	}
	if _, _, err = right.Integrate(ctx, "shared", bundle); err == nil {
		t.Fatal("tampered bundle succeeded")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testWorkspace() *config.Workspace {
	return &config.Workspace{
		Meta:     config.Meta{Version: 1},
		Groups:   map[string]config.Group{},
		Projects: map[string]config.Project{},
		Aliases:  map[string]string{"editor": "vim"},
	}
}

func cloneState(t *testing.T, workspace *config.Workspace) *config.Workspace {
	t.Helper()
	body, err := config.EncodeWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := config.DecodeStoredWorkspace(body)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func updateAlias(t *testing.T, store *Store, root, name, value string) {
	t.Helper()
	if _, err := store.Mutate(context.Background(), root, func(workspace *config.Workspace) error {
		workspace.Aliases[name] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
