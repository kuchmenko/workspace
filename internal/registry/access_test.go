package registry

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
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

func TestNetworkAdminRecoversWorkspaceAfterSoleAdminRemoval(t *testing.T) {
	ctx := context.Background()
	left := openTestStore(t)
	right := openTestStore(t)
	if _, err := left.EnsureNetwork(ctx, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := left.AddNetworkDevice(ctx, "right", right.identity.PublicKey(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	network, err := left.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.ImportNetwork(ctx, network, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = left.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin, right.identity.ID(): WorkspaceWriter}}
	if _, err = left.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	bundle, err := left.ExportFor(ctx, "shared", right.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.AttachFrom(ctx, "shared", t.TempDir(), bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = right.RemoveNetworkDevice(ctx, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	recovered, err := right.Access(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Role(right.identity.ID(), true) != WorkspaceAdmin || recovered.Role(left.identity.ID(), false) != "" {
		t.Fatalf("recovered policy = %#v", recovered)
	}
	workspace, err := right.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	var kind string
	if err = right.db.QueryRow(`SELECT kind FROM revisions WHERE id=?`, workspace.Head).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "access-recovery" {
		t.Fatalf("recovery revision kind = %q", kind)
	}
	network, err = right.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ratified := false
	for _, event := range network.Events {
		ratified = ratified || event.Action == "recover" && containsString(event.RecoveryIDs, workspace.Head)
	}
	if !ratified {
		t.Fatal("workspace recovery was not ratified by network history")
	}
	member := testIdentity(t)
	if _, err = right.AddNetworkDevice(ctx, "member", member.PublicKey(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	bundle, err = right.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.validateBundle(ctx, bundle, true); err != nil {
		t.Fatalf("historical ratified recovery failed after later network history: %v", err)
	}
}

func TestRemovedWriterRevisionIsRejectedThroughActiveRelay(t *testing.T) {
	ctx := context.Background()
	admin := openTestStore(t)
	writer := openTestStore(t)
	relay := openTestStore(t)
	connectRegistryStores(t, admin, writer, relay)
	adminRoot, writerRoot, relayRoot := t.TempDir(), t.TempDir(), t.TempDir()
	if _, err := admin.Create(ctx, "shared", adminRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{admin.identity.ID(): WorkspaceAdmin, writer.identity.ID(): WorkspaceWriter, relay.identity.ID(): WorkspaceReplica}}
	if _, err := admin.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	writerBundle, err := admin.ExportFor(ctx, "shared", writer.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.AttachFrom(ctx, "shared", writerRoot, writerBundle, admin.identity.ID()); err != nil {
		t.Fatal(err)
	}
	relayBundle, err := admin.ExportFor(ctx, "shared", relay.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = relay.AttachFrom(ctx, "shared", relayRoot, relayBundle, admin.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.RemoveNetworkDevice(ctx, writer.identity.ID()); err != nil {
		t.Fatal(err)
	}
	updateAlias(t, writer, writerRoot, "post-removal", "writer")
	stale, err := writer.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = relay.IntegrateFrom(ctx, "shared", stale, writer.identity.ID()); err != nil {
		t.Fatal(err)
	}
	network, err := admin.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = relay.MergeNetwork(ctx, network); err != nil {
		t.Fatal(err)
	}
	relayed, err := relay.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = admin.IntegrateFrom(ctx, "shared", relayed, relay.identity.ID()); err == nil || !strings.Contains(err.Error(), "workspace epoch is stale") {
		t.Fatalf("relayed removed-writer revision error = %v", err)
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

func TestConcurrentWorkspaceAccessChangesPersistAndResolve(t *testing.T) {
	tests := []struct {
		name  string
		left  func(AccessPolicy, string, string) AccessPolicy
		right func(AccessPolicy, string, string) AccessPolicy
	}{
		{
			name: "add add",
			left: func(policy AccessPolicy, third, _ string) AccessPolicy {
				policy.Roles[third] = WorkspaceWriter
				return policy
			},
			right: func(policy AccessPolicy, _, fourth string) AccessPolicy {
				policy.Roles[fourth] = WorkspaceReplica
				return policy
			},
		},
		{
			name: "demotion add",
			left: func(policy AccessPolicy, _, _ string) AccessPolicy {
				for id, role := range policy.Roles {
					if role == WorkspaceAdmin {
						policy.Roles[id] = WorkspaceWriter
						break
					}
				}
				return policy
			},
			right: func(policy AccessPolicy, third, _ string) AccessPolicy {
				policy.Roles[third] = WorkspaceWriter
				return policy
			},
		},
		{
			name: "revocation change",
			left: func(policy AccessPolicy, third, _ string) AccessPolicy {
				delete(policy.Roles, third)
				policy.Denied = append(policy.Denied, third)
				return policy
			},
			right: func(policy AccessPolicy, third, _ string) AccessPolicy {
				policy.Roles[third] = WorkspaceReplica
				return policy
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			left := openTestStore(t)
			right := openTestStore(t)
			third := testIdentity(t)
			fourth := testIdentity(t)
			if _, err := left.EnsureNetwork(ctx, "left"); err != nil {
				t.Fatal(err)
			}
			if _, err := left.AddNetworkDevice(ctx, "right", right.identity.PublicKey(), NetworkAdmin); err != nil {
				t.Fatal(err)
			}
			if _, err := left.AddNetworkDevice(ctx, "third", third.PublicKey(), NetworkMember); err != nil {
				t.Fatal(err)
			}
			if _, err := left.AddNetworkDevice(ctx, "fourth", fourth.PublicKey(), NetworkMember); err != nil {
				t.Fatal(err)
			}
			network, err := left.ExportNetwork(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = right.ImportNetwork(ctx, network, left.identity.ID()); err != nil {
				t.Fatal(err)
			}
			if _, err = left.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
				t.Fatal(err)
			}
			basePolicy := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin, right.identity.ID(): WorkspaceAdmin}}
			if test.name == "revocation change" {
				basePolicy.Roles[third.ID()] = WorkspaceWriter
			}
			if _, err = left.SetAccess(ctx, "shared", basePolicy); err != nil {
				t.Fatal(err)
			}
			baseBundle, err := left.ExportFor(ctx, "shared", right.identity.ID())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = right.AttachFrom(ctx, "shared", t.TempDir(), baseBundle, left.identity.ID()); err != nil {
				t.Fatal(err)
			}
			leftPolicy := test.left(cloneAccessPolicy(basePolicy), third.ID(), fourth.ID())
			rightPolicy := test.right(cloneAccessPolicy(basePolicy), third.ID(), fourth.ID())
			if test.name == "demotion add" {
				leftPolicy.Roles[right.identity.ID()] = WorkspaceWriter
				leftPolicy.Roles[left.identity.ID()] = WorkspaceAdmin
			}
			if _, err = left.SetAccess(ctx, "shared", leftPolicy); err != nil {
				t.Fatal(err)
			}
			if _, err = right.SetAccess(ctx, "shared", rightPolicy); err != nil {
				t.Fatal(err)
			}
			leftBranch, _ := left.Export(ctx, "shared")
			rightBranch, _ := right.Export(ctx, "shared")
			if _, _, err = left.IntegrateFrom(ctx, "shared", rightBranch, right.identity.ID()); !errors.Is(err, ErrWorkspaceAccessConflict) {
				t.Fatalf("integration error = %v", err)
			}
			conflict, err := left.AccessConflict(ctx, "shared")
			if err != nil {
				t.Fatal(err)
			}
			if len(conflict.Heads) != 2 {
				t.Fatalf("access conflict = %#v", conflict)
			}
			leftPath := left.path
			if err = left.Close(); err != nil {
				t.Fatal(err)
			}
			left, err = Open(leftPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = left.Close() }()
			conflict, err = left.AccessConflict(ctx, "shared")
			if err != nil {
				t.Fatal(err)
			}
			var selected string
			for _, head := range conflict.Heads {
				if head.ID == leftBranch.Heads[0] {
					selected = head.ID
				}
			}
			if selected == "" {
				t.Fatal("left access head not found")
			}
			if _, err = left.ResolveAccessConflict(ctx, "shared", conflict.ID, selected, selected); err != nil {
				t.Fatal(err)
			}
			resolved, err := left.Export(ctx, "shared")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = right.IntegrateFrom(ctx, "shared", resolved, left.identity.ID()); err != nil {
				t.Fatalf("resolved access history did not converge: %v", err)
			}
			if _, _, err = left.IntegrateFrom(ctx, "shared", rightBranch, right.identity.ID()); err != nil {
				t.Fatalf("old access branch reopened conflict: %v", err)
			}
			parent := revisionFromBundle(t, rightBranch, rightBranch.Heads[0])
			stalePolicy := cloneAccessPolicy(*parent.Access)
			stalePolicy.Mode = AccessAll
			stalePolicy.DefaultRole = WorkspaceWriter
			staleEpoch := parent.Epoch
			if policyRestricts(*parent.Access, stalePolicy) {
				staleEpoch++
			}
			staleRevision, err := makeRevision(parent.WorkspaceID, staleEpoch, "access", []string{parent.ID}, parent.Snapshot, parent.Conflicts, stalePolicy, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			staleBranch := rightBranch
			staleBranch.Epoch = staleRevision.Epoch
			staleBranch.Heads = []string{staleRevision.ID}
			staleBranch.Revisions = append(staleBranch.Revisions, staleRevision)
			if _, _, err = left.IntegrateFrom(ctx, "shared", staleBranch, right.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot reopen") {
				t.Fatalf("new stale access branch reopened conflict: %v", err)
			}
			selectedPolicy := revisionFromBundle(t, resolved, resolved.Heads[0]).Access
			equalEpoch := parent.Epoch
			if policyRestricts(*parent.Access, *selectedPolicy) {
				equalEpoch++
			}
			equalPolicyStale, err := makeRevision(parent.WorkspaceID, equalEpoch, "access", []string{parent.ID}, parent.Snapshot, parent.Conflicts, *selectedPolicy, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			equalPolicyBranch := rightBranch
			equalPolicyBranch.Epoch = equalPolicyStale.Epoch
			equalPolicyBranch.Heads = []string{equalPolicyStale.ID}
			equalPolicyBranch.Revisions = append(equalPolicyBranch.Revisions, equalPolicyStale)
			if _, _, err = left.IntegrateFrom(ctx, "shared", equalPolicyBranch, right.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot reopen") {
				t.Fatalf("equal-policy stale branch grafted into resolved history: %v", err)
			}
			resolvedHead := revisionFromBundle(t, resolved, resolved.Heads[0])
			graft, err := makeRevision(parent.WorkspaceID, max(resolvedHead.Epoch, staleRevision.Epoch)+1, "access-resolution", []string{resolvedHead.ID, staleRevision.ID}, staleRevision.Snapshot, staleRevision.Conflicts, *staleRevision.Access, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			graftedRevisions := map[string]Revision{graft.ID: graft, staleRevision.ID: staleRevision}
			for _, revision := range resolved.Revisions {
				graftedRevisions[revision.ID] = revision
			}
			for _, revision := range staleBranch.Revisions {
				graftedRevisions[revision.ID] = revision
			}
			grafted := Bundle{WorkspaceID: resolved.WorkspaceID, Epoch: graft.Epoch, Heads: []string{graft.ID}}
			for _, revision := range graftedRevisions {
				grafted.Revisions = append(grafted.Revisions, revision)
			}
			if _, _, err = left.IntegrateFrom(ctx, "shared", grafted, right.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot graft") {
				t.Fatalf("stale access branch grafted through a resolution: %v", err)
			}
			launder, err := makeRevision(parent.WorkspaceID, staleRevision.Epoch+1, "access-resolution", []string{parent.ID, staleRevision.ID}, staleRevision.Snapshot, staleRevision.Conflicts, *staleRevision.Access, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			nested, err := makeRevision(parent.WorkspaceID, max(resolvedHead.Epoch, launder.Epoch)+1, "access-resolution", []string{resolvedHead.ID, launder.ID}, launder.Snapshot, launder.Conflicts, *launder.Access, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			graftedRevisions[launder.ID] = launder
			graftedRevisions[nested.ID] = nested
			grafted.Epoch = nested.Epoch
			grafted.Heads = []string{nested.ID}
			grafted.Revisions = grafted.Revisions[:0]
			for _, revision := range graftedRevisions {
				grafted.Revisions = append(grafted.Revisions, revision)
			}
			if _, _, err = left.IntegrateFrom(ctx, "shared", grafted, right.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot graft") {
				t.Fatalf("stale access branch laundered through nested resolutions: %v", err)
			}
			merge, err := makeRevision(parent.WorkspaceID, max(resolvedHead.Epoch, launder.Epoch), "merge", []string{resolvedHead.ID, launder.ID}, launder.Snapshot, launder.Conflicts, *launder.Access, right.identity)
			if err != nil {
				t.Fatal(err)
			}
			delete(graftedRevisions, graft.ID)
			delete(graftedRevisions, nested.ID)
			graftedRevisions[merge.ID] = merge
			grafted.Epoch = merge.Epoch
			grafted.Heads = []string{merge.ID}
			grafted.Revisions = grafted.Revisions[:0]
			for _, revision := range graftedRevisions {
				grafted.Revisions = append(grafted.Revisions, revision)
			}
			if _, _, err = left.IntegrateFrom(ctx, "shared", grafted, right.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot graft") {
				t.Fatalf("stale access branch laundered through a pre-authored merge: %v", err)
			}
			if _, err = left.AccessConflict(ctx, "shared"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("resolved conflict lookup = %v", err)
			}
		})
	}
}

func revisionFromBundle(t *testing.T, bundle Bundle, id string) Revision {
	t.Helper()
	for _, revision := range bundle.Revisions {
		if revision.ID == id {
			return revision
		}
	}
	t.Fatalf("revision %s not found", id)
	return Revision{}
}

func testIdentity(t *testing.T) device.Identity {
	t.Helper()
	identity, err := device.Load(filepath.Join(t.TempDir(), "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	return identity
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
