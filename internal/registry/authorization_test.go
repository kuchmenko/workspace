package registry

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kuchmenko/workspace/internal/device"
)

func TestWorkspaceAuthorizationRejectsUnauthorizedRevisions(t *testing.T) {
	tests := []struct {
		name  string
		forge func(*testing.T, *Store, *Store, Bundle) Bundle
	}{
		{name: "writer access change", forge: forgedWriterAccess},
		{name: "unknown writer", forge: forgedUnknownWriter},
		{name: "missing parent", forge: forgedMissingParent},
		{name: "tampered policy", forge: tamperedPolicy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			left, right, bundle := sharedWriterBundle(t)
			forged := test.forge(t, left, right, bundle)
			if _, _, err := left.IntegrateFrom(ctx, "shared", forged, right.identity.ID()); err == nil {
				t.Fatal("unauthorized bundle was accepted")
			}
		})
	}
}

func TestReplicaForwardsDivergentHeadsWithoutAuthoringMerge(t *testing.T) {
	ctx := context.Background()
	admin := openTestStore(t)
	writer := openTestStore(t)
	replica := openTestStore(t)
	connectRegistryStores(t, admin, writer, replica)
	adminRoot, writerRoot, replicaRoot := t.TempDir(), t.TempDir(), t.TempDir()
	if _, err := admin.Create(ctx, "shared", adminRoot, testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{
		admin.identity.ID():   WorkspaceAdmin,
		writer.identity.ID():  WorkspaceWriter,
		replica.identity.ID(): WorkspaceReplica,
	}}
	if _, err := admin.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	initial, err := admin.ExportFor(ctx, "shared", writer.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.AttachFrom(ctx, "shared", writerRoot, initial, admin.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = replica.AttachFrom(ctx, "shared", replicaRoot, initial, admin.identity.ID()); err != nil {
		t.Fatal(err)
	}
	updateAlias(t, admin, adminRoot, "from-admin", "yes")
	updateAlias(t, writer, writerRoot, "from-writer", "yes")
	adminBundle, err := admin.ExportFor(ctx, "shared", replica.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = replica.IntegrateFrom(ctx, "shared", adminBundle, admin.identity.ID()); err != nil {
		t.Fatal(err)
	}
	writerBundle, err := writer.ExportFor(ctx, "shared", replica.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := replica.IntegrateFrom(ctx, "shared", writerBundle, writer.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Aliases["from-writer"] != "" {
		t.Fatalf("replica selected divergent writer state: %#v", stored.State.Aliases)
	}
	forwarded, err := replica.ExportFor(ctx, "shared", admin.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(forwarded.Heads) != 2 {
		t.Fatalf("replica heads = %v", forwarded.Heads)
	}
	unresolved, err := replica.HasUnresolvedConflicts(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if !unresolved {
		t.Fatal("replica with divergent heads was reported as resolved")
	}
	for _, revision := range forwarded.Revisions {
		for _, proof := range revision.Proofs {
			if proof.DeviceID == replica.identity.ID() {
				t.Fatalf("replica authored revision %s", revision.ID)
			}
		}
	}
	merged, conflicts, err := admin.IntegrateFrom(ctx, "shared", forwarded, replica.identity.ID())
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("writer merge conflicts=%v error=%v", conflicts, err)
	}
	if merged.State.Aliases["from-admin"] != "yes" || merged.State.Aliases["from-writer"] != "yes" {
		t.Fatalf("merged state = %#v", merged.State.Aliases)
	}
	mergedBundle, err := admin.ExportFor(ctx, "shared", replica.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	converged, conflicts, err := replica.IntegrateFrom(ctx, "shared", mergedBundle, admin.identity.ID())
	if err != nil || len(conflicts) != 0 || converged.Head != merged.Head {
		t.Fatalf("replica convergence workspace=%#v conflicts=%v error=%v", converged, conflicts, err)
	}
}

func TestValidateBundleHeadsAllowsMatchingPoliciesAtSameOlderEpoch(t *testing.T) {
	oldPolicy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter}
	newPolicy := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{"admin": WorkspaceAdmin}}
	revisions := map[string]Revision{
		"a": {ID: "a", Epoch: 1, Access: &oldPolicy},
		"b": {ID: "b", Epoch: 2, Access: &newPolicy},
		"c": {ID: "c", Epoch: 1, Access: &oldPolicy},
	}
	heads, err := validateBundleHeads(Bundle{Epoch: 2, Heads: []string{"a", "b", "c"}}, revisions)
	if err != nil {
		t.Fatalf("valid old/new/old head frontier rejected: %v", err)
	}
	if !equalStrings(heads, []string{"a", "b", "c"}) {
		t.Fatalf("heads = %v", heads)
	}
}

func TestWorkspaceBundlesAreReorderedAndRepeatedIdempotently(t *testing.T) {
	ctx := context.Background()
	left, right, bundle := sharedWriterBundle(t)
	slices.Reverse(bundle.Revisions)
	root := t.TempDir()
	if _, err := right.AttachFrom(ctx, "shared", root, bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	before := revisionCount(t, right)
	if _, _, err := right.IntegrateFrom(ctx, "shared", bundle, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if after := revisionCount(t, right); after != before {
		t.Fatalf("repeated bundle revisions = %d, want %d", after, before)
	}
}

func sharedWriterBundle(t *testing.T) (*Store, *Store, Bundle) {
	t.Helper()
	ctx := context.Background()
	left, right := pairedRegistryStores(t)
	if _, err := left.Create(ctx, "shared", t.TempDir(), testWorkspace()); err != nil {
		t.Fatal(err)
	}
	policy := AccessPolicy{Mode: AccessAll, DefaultRole: WorkspaceWriter, Roles: map[string]string{left.identity.ID(): WorkspaceAdmin}}
	if _, err := left.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	bundle, err := left.ExportFor(ctx, "shared", right.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	return left, right, bundle
}

func forgedWriterAccess(t *testing.T, _ *Store, right *Store, bundle Bundle) Bundle {
	t.Helper()
	parent := bundle.Heads[0]
	policy := copiedPolicy(revisionPolicy(bundle.Revisions, parent))
	policy.Roles[right.identity.ID()] = WorkspaceAdmin
	revision, err := makeRevision(bundle.WorkspaceID, bundle.Epoch, "access", []string{parent}, revisionSnapshot(t, bundle, parent), nil, policy, right.identity)
	if err != nil {
		t.Fatal(err)
	}
	return appendBundleHead(bundle, revision)
}

func copiedPolicy(policy AccessPolicy) AccessPolicy {
	roles := make(map[string]string, len(policy.Roles))
	for deviceID, role := range policy.Roles {
		roles[deviceID] = role
	}
	policy.Roles = roles
	policy.Denied = append([]string(nil), policy.Denied...)
	return policy
}

func forgedUnknownWriter(t *testing.T, _ *Store, _ *Store, bundle Bundle) Bundle {
	t.Helper()
	unknown, err := device.Load(filepath.Join(t.TempDir(), "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	parent := bundle.Heads[0]
	revision, err := makeRevision(bundle.WorkspaceID, bundle.Epoch, "ordinary", []string{parent}, revisionSnapshot(t, bundle, parent), nil, revisionPolicy(bundle.Revisions, parent), unknown)
	if err != nil {
		t.Fatal(err)
	}
	return appendBundleHead(bundle, revision)
}

func forgedMissingParent(t *testing.T, _ *Store, right *Store, bundle Bundle) Bundle {
	t.Helper()
	parent := bundle.Heads[0]
	revision, err := makeRevision(bundle.WorkspaceID, bundle.Epoch, "ordinary", []string{"missing"}, revisionSnapshot(t, bundle, parent), nil, revisionPolicy(bundle.Revisions, parent), right.identity)
	if err != nil {
		t.Fatal(err)
	}
	return appendBundleHead(bundle, revision)
}

func tamperedPolicy(t *testing.T, _ *Store, _ *Store, bundle Bundle) Bundle {
	t.Helper()
	for index := range bundle.Revisions {
		if bundle.Revisions[index].ID == bundle.Heads[0] {
			bundle.Revisions[index].Access.DefaultRole = WorkspaceReplica
		}
	}
	return bundle
}

func appendBundleHead(bundle Bundle, revision Revision) Bundle {
	bundle.Revisions = append(bundle.Revisions, revision)
	bundle.Heads = []string{revision.ID}
	bundle.Epoch = revision.Epoch
	return bundle
}

func revisionSnapshot(t *testing.T, bundle Bundle, id string) []byte {
	t.Helper()
	for _, revision := range bundle.Revisions {
		if revision.ID == id {
			return revision.Snapshot
		}
	}
	t.Fatalf("revision %s not found", id)
	return nil
}

func revisionCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM revisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func connectRegistryStores(t *testing.T, owner *Store, others ...*Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := owner.EnsureNetwork(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	for index, store := range others {
		if _, err := owner.AddNetworkDevice(ctx, string(rune('a'+index)), store.identity.PublicKey(), NetworkMember); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := owner.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range others {
		if _, err = store.ImportNetwork(ctx, bundle, owner.identity.ID()); err != nil {
			t.Fatal(err)
		}
	}
}
