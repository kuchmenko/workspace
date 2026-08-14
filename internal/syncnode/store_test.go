package syncnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncprotocol"
)

func TestImportPersistsGenesisWorkspace(t *testing.T) {
	directory := t.TempDir()
	identity, err := OpenOrCreateIdentity(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	recoveryPublicKey, err := CreateRecoveryKey(filepath.Join(directory, "recovery.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(directory, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(directory, "workspace")
	if err = os.Mkdir(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &config.Workspace{
		Meta:     config.Meta{Version: 1, Root: workspaceRoot},
		Groups:   map[string]config.Group{},
		Projects: map[string]config.Project{},
		Aliases:  map[string]string{"ws": "workspace"},
	}
	imported, err := store.Import(context.Background(), "personal", workspaceRoot, state, identity, recoveryPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if imported.ID == ([32]byte{}) || imported.Epoch == ([32]byte{}) || imported.Head == ([32]byte{}) {
		t.Fatal("import did not create protocol identities")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(filepath.Join(directory, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.LoadByName(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Head != imported.Head || loaded.Root != workspaceRoot || !reflect.DeepEqual(loaded.State.Aliases, state.Aliases) {
		t.Fatalf("loaded workspace mismatch\nimported %#v\nloaded %#v", imported, loaded)
	}
	listed, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "personal" {
		t.Fatalf("listed workspaces = %#v", listed)
	}
}

func TestIdentityPersistsAndRecoveryKeyDoesNotOverwrite(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "identity.key")
	first, err := OpenOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.NodeID() != second.NodeID() {
		t.Fatal("node identity changed after reload")
	}
	recovery := filepath.Join(directory, "recovery.key")
	if _, err = CreateRecoveryKey(recovery); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateRecoveryKey(recovery); err == nil {
		t.Fatal("existing recovery key was overwritten")
	}
}

func TestCommitCreatesSignedChildAndRejectsStaleHead(t *testing.T) {
	directory := t.TempDir()
	identity, err := OpenOrCreateIdentity(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	recoveryPublicKey, err := CreateRecoveryKey(filepath.Join(directory, "recovery.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(directory, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := filepath.Join(directory, "workspace")
	if err = os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	genesis, err := store.Import(context.Background(), "personal", root, state, identity, recoveryPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	state.Projects["app"] = config.Project{Path: "personal/app", Remote: "git@github.com:owner/app.git", Status: config.StatusActive, Category: config.CategoryPersonal}
	committed, err := store.Commit(context.Background(), "personal", genesis.Head, state, identity)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Head == genesis.Head {
		t.Fatal("head did not change")
	}
	var coreBytes, signature []byte
	if err = store.db.QueryRow(`SELECT r.core,p.signature FROM revisions r JOIN revision_proofs p ON p.revision_id=r.revision_id WHERE r.revision_id=?`, committed.Head[:]).Scan(&coreBytes, &signature); err != nil {
		t.Fatal(err)
	}
	core, err := syncprotocol.DecodeRevisionCore(coreBytes)
	if err != nil {
		t.Fatal(err)
	}
	if core.Kind != syncprotocol.RevisionAuthority || len(core.Parents) != 1 || core.Parents[0] != genesis.Head {
		t.Fatalf("revision core = %#v", core)
	}
	proof := syncprotocol.SignatureProof{NodeID: identity.NodeID(), Signature: signature}
	if !syncprotocol.VerifyRevisionProof(identity.PublicKey(), committed.Head, proof) {
		t.Fatal("stored revision proof is invalid")
	}
	if _, err = store.Commit(context.Background(), "personal", genesis.Head, state, identity); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("stale commit error = %v", err)
	}
	loaded, err := store.LoadByRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Head != committed.Head || loaded.State.Projects["app"].Remote != state.Projects["app"].Remote {
		t.Fatalf("loaded workspace = %#v", loaded)
	}
}
