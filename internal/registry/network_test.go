package registry

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/device"
)

func TestNetworkPairingBundleConvergesAndAuthorizesAdmins(t *testing.T) {
	ctx := context.Background()
	arch := openTestStore(t)
	asahi := openTestStore(t)
	created, err := arch.EnsureNetwork(ctx, "arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Devices) != 1 || created.Devices[0].Role != NetworkAdmin {
		t.Fatalf("created network = %#v", created)
	}
	archState, err := arch.AddNetworkDevice(ctx, "asahi", asahi.identity.PublicKey(), NetworkAdmin)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := arch.AddNetworkDevice(ctx, "asahi", asahi.identity.PublicKey(), NetworkAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retried, archState) {
		t.Fatalf("idempotent add = %#v, want %#v", retried, archState)
	}
	bundle, err := arch.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	asahiState, err := asahi.ImportNetwork(ctx, bundle, arch.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archState, asahiState) {
		t.Fatalf("network states differ:\narch=%#v\nasahi=%#v", archState, asahiState)
	}

	lxc := openTestStore(t)
	if _, err = asahi.AddNetworkDevice(ctx, "lxc", lxc.identity.PublicKey(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	bundle, err = asahi.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lxc.ImportNetwork(ctx, bundle, asahi.identity.ID()); err != nil {
		t.Fatal(err)
	}
	lxcState, err := lxc.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lxcRecord, found := findDevice(lxcState.Devices, lxc.identity.ID())
	if !found || lxcRecord.Role != NetworkMember || !lxcRecord.Active {
		t.Fatalf("lxc record = %#v", lxcRecord)
	}
}

func TestNetworkRoleChangeRejectsHistoryBeyondPeerFrameLimit(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	large := testIdentity(t)
	if _, err := store.EnsureNetwork(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddNetworkDevice(ctx, strings.Repeat("n", 5<<20), large.PublicKey(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{NetworkAdmin, NetworkMember} {
		if _, err := store.SetNetworkRole(ctx, large.ID(), role); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetNetworkRole(ctx, large.ID(), NetworkAdmin); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized network history error = %v", err)
	}
	after, err := store.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Events) != len(before.Events) || after.Epoch != before.Epoch {
		t.Fatalf("oversized network event was persisted: before=%d/%d after=%d/%d", before.Epoch, len(before.Events), after.Epoch, len(after.Events))
	}
}

func TestNetworkRoleAndRemovalAdvanceSignedState(t *testing.T) {
	ctx := context.Background()
	arch := openTestStore(t)
	asahi := openTestStore(t)
	if _, err := arch.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	if _, err := arch.AddNetworkDevice(ctx, "asahi", asahi.identity.PublicKey(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	state, err := arch.SetNetworkRole(ctx, asahi.identity.ID(), NetworkAdmin)
	if err != nil {
		t.Fatal(err)
	}
	record, found := findDevice(state.Devices, asahi.identity.ID())
	if !found || record.Role != NetworkAdmin {
		t.Fatalf("promoted record = %#v", record)
	}
	state, err = arch.RemoveNetworkDevice(ctx, asahi.identity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if state.Epoch != 4 {
		t.Fatalf("network epoch = %d, want 4", state.Epoch)
	}
	record, found = findDevice(state.Devices, asahi.identity.ID())
	if !found || record.Active {
		t.Fatalf("removed record = %#v", record)
	}
	if _, err = arch.RemoveNetworkDevice(ctx, arch.identity.ID()); err == nil {
		t.Fatal("last admin removal succeeded")
	}
}

func TestConcurrentAdminRemovalsPersistAndResolveConflict(t *testing.T) {
	ctx := context.Background()
	arch := openTestStore(t)
	asahi := openTestStore(t)
	if _, err := arch.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	if _, err := arch.AddNetworkDevice(ctx, "asahi", asahi.identity.PublicKey(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	base, err := arch.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = asahi.ImportNetwork(ctx, base, arch.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = arch.RemoveNetworkDevice(ctx, asahi.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = asahi.RemoveNetworkDevice(ctx, arch.identity.ID()); err != nil {
		t.Fatal(err)
	}
	archBranch, err := arch.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	asahiBranch, err := asahi.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = arch.MergeNetworkFrom(ctx, asahiBranch, asahi.identity.ID()); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("merge error = %v, want network conflict", err)
	}
	if _, err = asahi.MergeNetworkFrom(ctx, archBranch, arch.identity.ID()); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("reverse merge error = %v, want network conflict", err)
	}
	archConflict, err := arch.NetworkConflict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	asahiConflict, err := asahi.NetworkConflict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if archConflict.ID != asahiConflict.ID || len(archConflict.Heads) != 2 {
		t.Fatalf("conflicts differ: arch=%#v asahi=%#v", archConflict, asahiConflict)
	}
	archPath := arch.path
	if err = arch.Close(); err != nil {
		t.Fatal(err)
	}
	arch, err = Open(archPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = arch.Close() }()
	archConflict, err = arch.NetworkConflict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := arch.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activeAdminCount(state.Devices) != 2 {
		t.Fatalf("pre-conflict state = %#v", state)
	}
	var selected string
	for _, head := range archConflict.Heads {
		if head.SignerID == arch.identity.ID() {
			selected = head.ID
		}
	}
	if selected == "" {
		t.Fatal("arch conflict head not found")
	}
	resolved, err := arch.ResolveNetworkConflict(ctx, archConflict.ID, selected)
	if err != nil {
		t.Fatal(err)
	}
	removed, found := findDevice(resolved.Devices, asahi.identity.ID())
	if !found || removed.Active {
		t.Fatalf("selected resolution state = %#v", resolved)
	}
	resolvedBundle, err := arch.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = asahi.MergeNetwork(ctx, resolvedBundle); err != nil {
		t.Fatal(err)
	}
	if _, err = arch.MergeNetwork(ctx, asahiBranch); err != nil {
		t.Fatalf("old conflicting bundle reopened conflict: %v", err)
	}
	added := testIdentity(t)
	staleHeads := networkFrontier(asahiBranch.Events)
	if len(staleHeads) != 1 {
		t.Fatalf("stale branch frontier = %v", staleHeads)
	}
	staleRecord := DeviceRecord{ID: added.ID(), Name: "stale-added", PublicKey: added.PublicKey(), Role: NetworkAdmin, Active: true}
	staleEvent, err := makeCausalNetworkEvent(asahiBranch.ID, asahiBranch.Epoch+1, "add", staleRecord, staleHeads, "", nil, asahi.identity)
	if err != nil {
		t.Fatal(err)
	}
	staleBranch := asahiBranch
	staleBranch.Epoch = staleEvent.Epoch
	staleBranch.Events = append(staleBranch.Events, staleEvent)
	if _, err = arch.MergeNetworkFrom(ctx, staleBranch, asahi.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot reopen") {
		t.Fatalf("inactive admin reopened resolved history: %v", err)
	}
	resolvedHeads := networkFrontier(resolvedBundle.Events)
	if len(resolvedHeads) != 1 {
		t.Fatalf("resolved frontier = %v", resolvedHeads)
	}
	graftParents := []string{resolvedHeads[0], staleEvent.ID}
	graft, err := makeCausalNetworkEvent(staleBranch.ID, max(resolvedBundle.Epoch, staleBranch.Epoch)+1, "resolve", DeviceRecord{}, graftParents, staleEvent.ID, nil, asahi.identity)
	if err != nil {
		t.Fatal(err)
	}
	grafted := combineNetworkBundles(resolvedBundle, staleBranch)
	grafted.Epoch = graft.Epoch
	grafted.Events = append(grafted.Events, graft)
	if _, err = arch.MergeNetworkFrom(ctx, grafted, asahi.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot graft") {
		t.Fatalf("inactive admin grafted rejected history through a resolution: %v", err)
	}
	launder, err := makeCausalNetworkEvent(staleBranch.ID, staleEvent.Epoch+1, "resolve", DeviceRecord{}, []string{staleHeads[0], staleEvent.ID}, staleEvent.ID, nil, asahi.identity)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := makeCausalNetworkEvent(staleBranch.ID, max(resolvedBundle.Epoch, launder.Epoch)+1, "resolve", DeviceRecord{}, []string{resolvedHeads[0], launder.ID}, launder.ID, nil, asahi.identity)
	if err != nil {
		t.Fatal(err)
	}
	nestedBundle := combineNetworkBundles(resolvedBundle, staleBranch)
	nestedBundle.Epoch = nested.Epoch
	nestedBundle.Events = append(nestedBundle.Events, launder, nested)
	if _, err = arch.MergeNetworkFrom(ctx, nestedBundle, asahi.identity.ID()); err == nil || !strings.Contains(err.Error(), "cannot graft") {
		t.Fatalf("inactive admin laundered rejected history through nested resolutions: %v", err)
	}
	if _, err = arch.NetworkConflict(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("resolved conflict lookup error = %v", err)
	}
}

func TestConcurrentAdminChangesBecomeDurableConflicts(t *testing.T) {
	tests := []struct {
		name  string
		left  func(context.Context, *Store, *Store, device.Identity) error
		right func(context.Context, *Store, *Store, device.Identity) error
	}{
		{
			name: "reciprocal demotions",
			left: func(ctx context.Context, left, right *Store, _ device.Identity) error {
				_, err := left.SetNetworkRole(ctx, right.identity.ID(), NetworkMember)
				return err
			},
			right: func(ctx context.Context, left, right *Store, _ device.Identity) error {
				_, err := right.SetNetworkRole(ctx, left.identity.ID(), NetworkMember)
				return err
			},
		},
		{
			name: "demotion versus admin add",
			left: func(ctx context.Context, left, right *Store, _ device.Identity) error {
				_, err := left.SetNetworkRole(ctx, right.identity.ID(), NetworkMember)
				return err
			},
			right: func(ctx context.Context, _, right *Store, added device.Identity) error {
				_, err := right.AddNetworkDevice(ctx, "added", added.PublicKey(), NetworkAdmin)
				return err
			},
		},
		{
			name: "removal versus stale admin add",
			left: func(ctx context.Context, left, right *Store, _ device.Identity) error {
				_, err := left.RemoveNetworkDevice(ctx, right.identity.ID())
				return err
			},
			right: func(ctx context.Context, _, right *Store, added device.Identity) error {
				_, err := right.AddNetworkDevice(ctx, "added", added.PublicKey(), NetworkAdmin)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			left := openTestStore(t)
			right := openTestStore(t)
			added := testIdentity(t)
			if _, err := left.EnsureNetwork(ctx, "left"); err != nil {
				t.Fatal(err)
			}
			if _, err := left.AddNetworkDevice(ctx, "right", right.identity.PublicKey(), NetworkAdmin); err != nil {
				t.Fatal(err)
			}
			base, err := left.ExportNetwork(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = right.ImportNetwork(ctx, base, left.identity.ID()); err != nil {
				t.Fatal(err)
			}
			if err = test.left(ctx, left, right, added); err != nil {
				t.Fatal(err)
			}
			if err = test.right(ctx, left, right, added); err != nil {
				t.Fatal(err)
			}
			leftBranch, _ := left.ExportNetwork(ctx)
			rightBranch, _ := right.ExportNetwork(ctx)
			if _, err = left.MergeNetwork(ctx, rightBranch); !errors.Is(err, ErrNetworkConflict) {
				t.Fatalf("left merge error = %v", err)
			}
			if _, err = right.MergeNetwork(ctx, leftBranch); !errors.Is(err, ErrNetworkConflict) {
				t.Fatalf("right merge error = %v", err)
			}
			leftConflict, err := left.NetworkConflict(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rightConflict, err := right.NetworkConflict(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if leftConflict.ID != rightConflict.ID || len(leftConflict.Heads) != 2 {
				t.Fatalf("durable conflicts differ: left=%#v right=%#v", leftConflict, rightConflict)
			}
		})
	}
}

func TestRecoveryAuthorityFollowsSelectedNetworkHistory(t *testing.T) {
	ctx := context.Background()
	left := openTestStore(t)
	right := openTestStore(t)
	rejected := testIdentity(t)
	if _, err := left.EnsureNetwork(ctx, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := left.AddNetworkDevice(ctx, "right", right.identity.PublicKey(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	base, err := left.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.ImportNetwork(ctx, base, left.identity.ID()); err != nil {
		t.Fatal(err)
	}
	baseHead, err := currentCausalNetworkHead(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = left.SetNetworkRole(ctx, right.identity.ID(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	if _, err = right.AddNetworkDevice(ctx, "rejected", rejected.PublicKey(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	rightBranch, _ := right.ExportNetwork(ctx)
	if _, err = left.MergeNetwork(ctx, rightBranch); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("merge error = %v", err)
	}
	conflict, err := left.NetworkConflict(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var selected string
	for _, head := range conflict.Heads {
		if head.SignerID == left.identity.ID() {
			selected = head.ID
		}
	}
	if _, err = left.ResolveNetworkConflict(ctx, conflict.ID, selected); err != nil {
		t.Fatal(err)
	}
	resolved, err := left.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policyWithoutAdmin := AccessPolicy{Mode: AccessSelected, Roles: map[string]string{rejected.ID(): WorkspaceWriter}}
	if recoveryAuthorizedByNetwork(resolved, "unratified", baseHead, rejected.ID(), policyWithoutAdmin) {
		t.Fatal("admin promoted only on the rejected branch received recovery authority")
	}
	if recoveryAuthorizedByNetwork(resolved, "unratified", baseHead, right.identity.ID(), policyWithoutAdmin) {
		t.Fatal("historical admin created an unratified backdated recovery")
	}
	if _, err = right.MergeNetwork(ctx, resolved); err != nil {
		t.Fatal(err)
	}
	if _, err = left.SetNetworkRole(ctx, right.identity.ID(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	shared, _ := left.ExportNetwork(ctx)
	if _, err = right.MergeNetwork(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if _, err = left.SetNetworkRole(ctx, right.identity.ID(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	if _, err = right.SetNetworkRole(ctx, left.identity.ID(), NetworkMember); err != nil {
		t.Fatal(err)
	}
	laterBranch, _ := right.ExportNetwork(ctx)
	if _, err = left.MergeNetwork(ctx, laterBranch); !errors.Is(err, ErrNetworkConflict) {
		t.Fatalf("new conflict after an earlier resolution was not retained: %v", err)
	}
}

func TestNetworkImportRejectsTamperedMembership(t *testing.T) {
	ctx := context.Background()
	arch := openTestStore(t)
	asahi := openTestStore(t)
	if _, err := arch.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	if _, err := arch.AddNetworkDevice(ctx, "asahi", asahi.identity.PublicKey(), NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	bundle, err := arch.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Events[len(bundle.Events)-1].DeviceName = "attacker"
	if _, err = asahi.ImportNetwork(ctx, bundle, arch.identity.ID()); err == nil {
		t.Fatal("tampered network bundle succeeded")
	}
}

func TestNetworkSchemaMigrationPreservesLegacyEvents(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "registry.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.EnsureNetwork(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`ALTER TABLE network_events RENAME TO network_events_new;
CREATE TABLE network_events (
 id TEXT PRIMARY KEY,
 network_id TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 action TEXT NOT NULL,
 device_id TEXT NOT NULL,
 device_name TEXT NOT NULL,
 device_public_key BLOB NOT NULL,
 role TEXT NOT NULL,
 signer_id TEXT NOT NULL,
 signer_public_key BLOB NOT NULL,
 signature BLOB NOT NULL
);
INSERT INTO network_events(id,network_id,epoch,action,device_id,device_name,device_public_key,role,signer_id,signer_public_key,signature)
SELECT id,network_id,epoch,action,device_id,device_name,device_public_key,role,signer_id,signer_public_key,signature FROM network_events_new;
DROP TABLE network_events_new;
DROP TABLE network_conflicts;`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	state, err := reopened.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, created) {
		t.Fatalf("migrated network = %#v, want %#v", state, created)
	}
}
