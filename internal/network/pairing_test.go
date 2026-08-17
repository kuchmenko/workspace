package network

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

type pairReady struct {
	code     string
	endpoint string
}

var networkTestStorePaths sync.Map

type pairOutcome struct {
	result PairResult
	err    error
}

func TestCanceledPairLeavesStoreUnpaired(t *testing.T) {
	store, identity := networkTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	outcome := make(chan error, 1)
	go func() {
		_, err := Pair(ctx, PairOptions{
			Store: store, Identity: identity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(string, string) { ready <- struct{}{} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		outcome <- err
	}()
	<-ready
	cancel()
	if err := <-outcome; !errors.Is(err, context.Canceled) {
		t.Fatalf("Pair error = %v, want context canceled", err)
	}
	if _, err := store.Network(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Network error = %v, want no network", err)
	}
}

func TestPairExchangesConfirmedPinnedIdentities(t *testing.T) {
	archStore, archIdentity := networkTestStore(t)
	asahiStore, asahiIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan pairReady, 1)
	serverOutcome := make(chan pairOutcome, 1)
	serverAuth := make(chan string, 1)
	serverConfirming := make(chan struct{})
	clientConfirming := make(chan struct{})
	go func() {
		result, err := Pair(ctx, PairOptions{
			Store:            archStore,
			Identity:         archIdentity,
			Name:             "arch",
			Role:             registry.NetworkAdmin,
			ListenAddress:    "127.0.0.1:0",
			DisableDiscovery: true,
			Ready:            func(code, endpoint string) { ready <- pairReady{code: code, endpoint: endpoint} },
			Confirm: func(peerName, authentication string) (bool, error) {
				if peerName != "asahi" {
					t.Errorf("server peer name = %q", peerName)
				}
				serverAuth <- authentication
				close(serverConfirming)
				select {
				case <-clientConfirming:
				case <-ctx.Done():
					return false, ctx.Err()
				}
				return true, nil
			},
		})
		serverOutcome <- pairOutcome{result: result, err: err}
	}()
	pairing := <-ready
	var clientAuth string
	clientResult, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
		Store:    asahiStore,
		Identity: asahiIdentity,
		Name:     "asahi",
		Code:     pairing.code,
		Confirm: func(peerName, authentication string) (bool, error) {
			if peerName != "arch" {
				t.Errorf("client peer name = %q", peerName)
			}
			clientAuth = authentication
			close(clientConfirming)
			select {
			case <-serverConfirming:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := <-serverOutcome
	if server.err != nil {
		t.Fatal(server.err)
	}
	if clientAuth == "" || clientAuth != <-serverAuth {
		t.Fatalf("authentication strings did not match: client=%q", clientAuth)
	}
	if clientResult.Peer.ID != archIdentity.ID() || server.result.Peer.ID != asahiIdentity.ID() {
		t.Fatalf("pair results client=%#v server=%#v", clientResult, server.result)
	}
	archNetwork, err := archStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	asahiNetwork, err := asahiStore.Network(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archNetwork, asahiNetwork) {
		t.Fatalf("network state differs:\narch=%#v\nasahi=%#v", archNetwork, asahiNetwork)
	}
}

func TestPairRejectsUnconfirmedJoinWithoutAddingDevice(t *testing.T) {
	archStore, archIdentity := networkTestStore(t)
	asahiStore, asahiIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan pairReady, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		_, err := Pair(ctx, PairOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(code, endpoint string) { ready <- pairReady{code: code, endpoint: endpoint} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		serverOutcome <- err
	}()
	pairing := <-ready
	if _, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
		Store: asahiStore, Identity: asahiIdentity, Name: "asahi", Code: pairing.code,
		Confirm: func(string, string) (bool, error) { return false, nil },
	}); err == nil {
		t.Fatal("unconfirmed join succeeded")
	}
	if err := <-serverOutcome; err == nil {
		t.Fatal("pair server accepted unconfirmed join")
	}
	if _, err := archStore.Network(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Network error = %v, want no network", err)
	}
}

func TestPairRejectsJoinerFromAnotherNetworkWithoutAddingDevice(t *testing.T) {
	archStore, archIdentity := networkTestStore(t)
	asahiStore, asahiIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := archStore.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	if _, err := asahiStore.EnsureNetwork(ctx, "asahi"); err != nil {
		t.Fatal(err)
	}
	ready := make(chan pairReady, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		_, err := Pair(ctx, PairOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(code, endpoint string) { ready <- pairReady{code: code, endpoint: endpoint} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		serverOutcome <- err
	}()
	pairing := <-ready
	if _, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
		Store: asahiStore, Identity: asahiIdentity, Name: "asahi", Code: pairing.code,
		Confirm: func(string, string) (bool, error) { return true, nil },
	}); err == nil {
		t.Fatal("cross-network join succeeded")
	}
	cancel()
	<-serverOutcome
	state, err := archStore.Network(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Devices) != 1 || state.Devices[0].ID != archIdentity.ID() {
		t.Fatalf("inviter network changed after cross-network join: %#v", state)
	}
}

func TestPairAllowsCorrectCodeAfterRejectedWrongCode(t *testing.T) {
	archStore, archIdentity := networkTestStore(t)
	asahiStore, asahiIdentity := networkTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan pairReady, 1)
	serverOutcome := make(chan error, 1)
	go func() {
		_, err := Pair(ctx, PairOptions{
			Store: archStore, Identity: archIdentity, Name: "arch",
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(code, endpoint string) { ready <- pairReady{code: code, endpoint: endpoint} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		serverOutcome <- err
	}()
	pairing := <-ready
	for attempt := 0; attempt < 10; attempt++ {
		connection, err := net.Dial("tcp", pairing.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}
	for attempt := 0; attempt < maxPairCodeFailures-1; attempt++ {
		if _, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
			Store: asahiStore, Identity: asahiIdentity, Name: "asahi", Code: "wrong-code",
			Confirm: func(string, string) (bool, error) { return true, nil },
		}); err == nil {
			t.Fatal("wrong pairing code succeeded")
		}
	}
	if _, err := JoinEndpoint(ctx, pairing.endpoint, JoinOptions{
		Store: asahiStore, Identity: asahiIdentity, Name: "asahi", Code: pairing.code,
		Confirm: func(string, string) (bool, error) { return true, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func networkTestStore(t *testing.T) (*registry.Store, device.Identity) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "registry.db")
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	networkTestStorePaths.Store(store, path)
	t.Cleanup(func() { _ = store.Close() })
	identity, err := device.Load(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	return store, identity
}
