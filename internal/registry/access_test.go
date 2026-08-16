package registry

import (
	"context"
	"slices"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestWorkspacePolicyValidationPreservesAnAdmin(t *testing.T) {
	admin := "admin"
	tests := []AccessPolicy{
		{Mode: AccessLocal, Roles: map[string]string{admin: WorkspaceAdmin, "peer": WorkspaceWriter}},
		{Mode: AccessSelected, Roles: map[string]string{"peer": WorkspaceWriter}},
		{Mode: AccessAll, DefaultRole: WorkspaceAdmin, Roles: map[string]string{admin: WorkspaceAdmin}},
		{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{admin: WorkspaceAdmin}, Denied: []string{admin}},
	}
	for _, policy := range tests {
		if _, err := normalizePolicy(policy); err == nil {
			t.Fatalf("invalid policy accepted: %#v", policy)
		}
	}
}

func TestWorkspaceAccessAuthorizesAttachAndRotatesEpochOnRevocation(t *testing.T) {
	ctx := context.Background()
	left, right := pairedRegistryStores(t)
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	if _, err := left.Create(ctx, "shared", leftRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	shared, err := left.SetAccess(ctx, "shared", policy)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := left.ExportFor(ctx, "shared", right.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.AttachFrom(ctx, "shared", rightRoot, bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	updateAlias(t, right, rightRoot, "stale", "right")
	stale, err := right.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	local := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	revoked, err := left.SetAccess(ctx, "shared", local)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Epoch != shared.Epoch+1 {
		t.Fatalf("revoked epoch = %d, want %d", revoked.Epoch, shared.Epoch+1)
	}
	if _, _, err = left.IntegrateFrom(ctx, "shared", stale, right.identity.ID()); err == nil {
		t.Fatal("stale writer revision was accepted")
	}
	var quarantined int
	if err = left.db.QueryRow(`SELECT COUNT(*) FROM workspace_quarantine WHERE workspace_id=? AND source_device_id=?`, revoked.WorkspaceID, right.identity.ID()).Scan(&quarantined); err != nil {
		t.Fatal(err)
	}
	if quarantined == 0 {
		t.Fatal("stale writer revision was not quarantined")
	}
}

func TestNetworkRemovalDeniesAllPolicyDevice(t *testing.T) {
	ctx := context.Background()
	left, right := pairedRegistryStores(t)
	if _, err := left.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	if _, err := left.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	before, err := left.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = left.RemoveNetworkDevice(ctx, right.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if err = left.ReconcileNetworkAccess(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := left.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	access, err := left.Access(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch != before.Epoch+1 || !slices.Contains(access.Denied, right.identity.ID()) {
		t.Fatalf("reconciled workspace=%#v access=%#v", after, access)
	}
	if _, err = left.ExportFor(ctx, "shared", right.identity.ID()); err == nil {
		t.Fatal("removed network device can still receive workspace")
	}
}

func TestWorkspaceSharingRejectsCredentialBearingRemotes(t *testing.T) {
	ctx := context.Background()
	left, right := pairedRegistryStores(t)
	state := testWorkspace()
	state.Projects["private"] = config.Project{
		Remote:   "https://user:secret@example.com/private.git",
		Path:     "private",
		Status:   config.StatusActive,
		Category: config.CategoryPersonal,
	}
	if _, err := left.Create(ctx, "private", t.TempDir(), state); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	if _, err := left.SetAccess(ctx, "private", policy); err == nil {
		t.Fatal("credential-bearing workspace was shared")
	}
	if _, err := left.ExportFor(ctx, "private", right.identity.ID()); err == nil {
		t.Fatal("credential-bearing workspace was exported")
	}
}

func pairedRegistryStores(t *testing.T) (*Store, *Store) {
	t.Helper()
	left := openTestStore(t)
	right := openTestStore(t)
	ctx := context.Background()
	if _, err := left.EnsureNetwork(ctx, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := left.AddNetworkDevice(ctx, "right", right.identity.PublicKey(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	bundle, err := left.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.ImportNetwork(ctx, bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	return left, right
}
