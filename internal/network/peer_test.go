package network

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

func TestWatchConnectionUnblocksPartialReadOnCancellation(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	stop := watchConnection(ctx, left)
	defer stop()
	done := make(chan error, 1)
	go func() {
		var request peerRequest
		done <- decodeLimited(left, maxPeerMessageBytes, &request)
	}()
	if _, err := right.Write([]byte(`{"version":`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("partial request decoded")
		}
	case <-time.After(time.Second):
		t.Fatal("connection read remained blocked after cancellation")
	}
}

func TestPeerProbeAuthenticatesPairedDevices(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := make(chan string, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		serverOutcome <- Serve(ctx, ServeOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready: func(address string) { endpoint <- address },
		})
	}()
	state, err := asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	arch, found := activeDevice(state.Devices, archIdentity.ID())
	if !found {
		t.Fatal("paired arch device not found")
	}
	info, err := Probe(ctx, <-endpoint, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceID != archIdentity.ID() || info.Name != "arch" || info.NetworkID != state.ID {
		t.Fatalf("peer info = %#v", info)
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func TestPeerProbeRejectsUnknownDevice(t *testing.T) {
	archStore, archIdentity, asahiStore, _ := pairedTestStores(t)
	_, unknownIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := make(chan string, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		serverOutcome <- Serve(ctx, ServeOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready: func(address string) { endpoint <- address },
		})
	}()
	state, err := asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	arch, _ := activeDevice(state.Devices, archIdentity.ID())
	if _, err = Probe(ctx, <-endpoint, arch, asahiStore, unknownIdentity, "unknown"); err == nil {
		t.Fatal("unknown device authenticated")
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func TestPeerProbeRejectsMismatchedPinnedIdentity(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	_, otherIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := make(chan string, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		serverOutcome <- Serve(ctx, ServeOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready: func(address string) { endpoint <- address },
		})
	}()
	state, err := asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	arch, _ := activeDevice(state.Devices, archIdentity.ID())
	arch.PublicKey = append([]byte(nil), otherIdentity.PublicKey()...)
	if _, err = Probe(ctx, <-endpoint, arch, asahiStore, asahiIdentity, "asahi"); err == nil {
		t.Fatal("mismatched server identity authenticated")
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func TestPeerProbeReplicatesSignedNetworkRole(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := archStore.SetNetworkRole(ctx, asahiIdentity.ID(), registry.NetworkAdmin); err != nil {
		t.Fatal(err)
	}
	endpoint := make(chan string, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		serverOutcome <- Serve(ctx, ServeOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready: func(address string) { endpoint <- address },
		})
	}()
	state, err := asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	arch, _ := activeDevice(state.Devices, archIdentity.ID())
	if _, err = Probe(ctx, <-endpoint, arch, asahiStore, asahiIdentity, "asahi"); err != nil {
		t.Fatal(err)
	}
	state, err = asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	asahi, found := activeDevice(state.Devices, asahiIdentity.ID())
	if !found || asahi.Role != registry.NetworkAdmin {
		t.Fatalf("replicated Asahi role = %#v", asahi)
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func pairedTestStores(t *testing.T) (*registry.Store, device.Identity, *registry.Store, device.Identity) {
	t.Helper()
	archStore, archIdentity := networkTestStore(t)
	asahiStore, asahiIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan pairReady, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		_, err := Pair(ctx, PairOptions{
			Store: archStore, Identity: archIdentity, Name: "arch", Role: registry.NetworkAdmin,
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(code, endpoint string) { ready <- pairReady{code: code, endpoint: endpoint} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		serverOutcome <- err
	}()
	pairing := <-ready
	_, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
		Store: asahiStore, Identity: asahiIdentity, Name: "asahi", Code: pairing.code,
		Confirm: func(string, string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
	return archStore, archIdentity, asahiStore, asahiIdentity
}
