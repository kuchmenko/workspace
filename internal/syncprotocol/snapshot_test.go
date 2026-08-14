package syncprotocol

import (
	"crypto/ed25519"
	"fmt"
	"reflect"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestWorkspaceSnapshotRoundTrip(t *testing.T) {
	publicKey := vectorPublicKey(t)
	nodeID, err := NodeIDFor(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{
		Meta:  config.Meta{Version: 1, Root: "/local/root"},
		Agent: config.AgentConfig{DefaultView: config.AgentViewFavorites},
		Groups: map[string]config.Group{
			"personal": {Description: "Personal", Favorite: true},
		},
		Projects: map[string]config.Project{
			"workspace": {
				Remote:        "git@github.com:kuchmenko/workspace.git",
				Mirrors:       map[string]string{},
				Path:          "personal/workspace",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
				Branches: []config.BranchMeta{{
					Name:     "feat/multi-master-sync",
					Machines: []string{"macbook", "archlinux", "archlinux"},
				}},
			},
		},
		Aliases: map[string]string{"ws": "workspace"},
	}
	snapshot, err := NewWorkspaceSnapshot(workspace, []Member{{NodeID: nodeID, PublicKey: publicKey, Role: RoleAdmin}}, 1, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeWorkspaceSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkspaceSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, decoded) {
		t.Fatalf("snapshot mismatch\nwant %#v\n got %#v", snapshot, decoded)
	}
	restored := decoded.Workspace("/new/root")
	if restored.Meta.Root != "/new/root" {
		t.Fatalf("restored root = %q", restored.Meta.Root)
	}
	if got := restored.Projects["workspace"].Branches[0].Machines; !reflect.DeepEqual(got, []string{"archlinux", "macbook"}) {
		t.Fatalf("normalized machines = %v", got)
	}
}

func TestWorkspaceSnapshotRejectsThresholdAndMemberMismatch(t *testing.T) {
	publicKey := vectorPublicKey(t)
	nodeID, err := NodeIDFor(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	snapshot, err := NewWorkspaceSnapshot(workspace, []Member{{NodeID: nodeID, PublicKey: publicKey, Role: RoleAdmin}}, 1, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.AdminThreshold = 2
	if _, err = EncodeWorkspaceSnapshot(snapshot); err == nil {
		t.Fatal("admin threshold beyond member count was accepted")
	}
	snapshot.AdminThreshold = 1
	snapshot.Members[0].NodeID[0] ^= 0xff
	if _, err = EncodeWorkspaceSnapshot(snapshot); err == nil {
		t.Fatal("member key mismatch was accepted")
	}
}

func TestWorkspaceSnapshotSupportsLargeRegistry(t *testing.T) {
	publicKey := vectorPublicKey(t)
	nodeID, err := NodeIDFor(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	for index := 0; index < 64; index++ {
		name := fmt.Sprintf("project-%02d", index)
		workspace.Projects[name] = config.Project{Path: "personal/" + name, Status: config.StatusActive, Category: config.CategoryPersonal}
	}
	snapshot, err := NewWorkspaceSnapshot(workspace, []Member{{NodeID: nodeID, PublicKey: publicKey, Role: RoleAdmin}}, 1, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeWorkspaceSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkspaceSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Registry.Projects) != 64 {
		t.Fatalf("projects = %d", len(decoded.Registry.Projects))
	}
}

func vectorPublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}
