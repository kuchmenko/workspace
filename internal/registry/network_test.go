package registry

import (
	"context"
	"reflect"
	"testing"
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
	if state.Epoch != 2 {
		t.Fatalf("network epoch = %d, want 2", state.Epoch)
	}
	record, found = findDevice(state.Devices, asahi.identity.ID())
	if !found || record.Active {
		t.Fatalf("removed record = %#v", record)
	}
	if _, err = arch.RemoveNetworkDevice(ctx, arch.identity.ID()); err == nil {
		t.Fatal("last admin removal succeeded")
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
