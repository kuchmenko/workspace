package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
)

type daemonEndpoints struct {
	sync.RWMutex
	left, right string
}

func TestRegistryAutoSyncUsesLowerDeviceIDInitiator(t *testing.T) {
	leftStore, leftIdentity := daemonTestStore(t)
	rightStore, rightIdentity := daemonTestStore(t)
	pairDaemonStores(t, leftStore, leftIdentity, rightStore, rightIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	_, err := leftStore.Create(ctx, "shared", leftRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{leftIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err = leftStore.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	bundle, err := leftStore.ExportFor(ctx, "shared", rightIdentity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rightStore.AttachFrom(ctx, "shared", rightRoot, bundle, leftIdentity.ID()); err != nil {
		t.Fatal(err)
	}

	endpoints := &daemonEndpoints{}
	leftPeer := daemonNetworkDevice(t, leftStore, rightIdentity.ID())
	rightPeer := daemonNetworkDevice(t, rightStore, leftIdentity.ID())
	var leftLogs, rightLogs strings.Builder
	var logMu sync.Mutex
	leftContext, stopLeft := context.WithCancel(ctx)
	rightContext, stopRight := context.WithCancel(ctx)
	leftDone, rightDone := make(chan error, 1), make(chan error, 1)
	go func() {
		leftDone <- Run(leftContext, Options{
			Store: leftStore, Identity: leftIdentity, Name: "left", ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			SyncInterval: time.Hour,
			Ready: func(endpoint string) {
				endpoints.Lock()
				endpoints.left = endpoint
				endpoints.Unlock()
			},
			Discover: func(context.Context) ([]peernetwork.PeerEndpoint, error) {
				endpoints.RLock()
				defer endpoints.RUnlock()
				if endpoints.right == "" {
					return nil, nil
				}
				return []peernetwork.PeerEndpoint{{Device: leftPeer, Endpoint: endpoints.right}}, nil
			},
			Logf: func(format string, args ...any) {
				logMu.Lock()
				defer logMu.Unlock()
				fmt.Fprintf(&leftLogs, format, args...)
				leftLogs.WriteByte('\n')
			},
		})
	}()
	go func() {
		rightDone <- Run(rightContext, Options{
			Store: rightStore, Identity: rightIdentity, Name: "right", ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			SyncInterval: time.Hour,
			Ready: func(endpoint string) {
				endpoints.Lock()
				endpoints.right = endpoint
				endpoints.Unlock()
			},
			Discover: func(context.Context) ([]peernetwork.PeerEndpoint, error) {
				endpoints.RLock()
				defer endpoints.RUnlock()
				if endpoints.left == "" {
					return nil, nil
				}
				return []peernetwork.PeerEndpoint{{Device: rightPeer, Endpoint: endpoints.left}}, nil
			},
			Logf: func(format string, args ...any) {
				logMu.Lock()
				defer logMu.Unlock()
				fmt.Fprintf(&rightLogs, format, args...)
				rightLogs.WriteByte('\n')
			},
		})
	}()
	waitForDaemonEndpoints(t, ctx, endpoints)
	waitForInitialDaemonSync(t, ctx, &logMu, &leftLogs, &rightLogs)
	time.Sleep(2 * defaultDebounce)
	logMu.Lock()
	leftLogs.Reset()
	rightLogs.Reset()
	logMu.Unlock()

	higherStore, higherRoot := rightStore, rightRoot
	if leftIdentity.ID() > rightIdentity.ID() {
		higherStore, higherRoot = leftStore, leftRoot
	}
	if _, err = higherStore.Mutate(ctx, higherRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["autosynced"] = "workspace"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitForDaemonSync(t, ctx, leftStore, rightStore)

	lowerLogs, higherLogs := &leftLogs, &rightLogs
	if leftIdentity.ID() > rightIdentity.ID() {
		lowerLogs, higherLogs = higherLogs, lowerLogs
	}
	waitForDaemonLog(t, ctx, &logMu, lowerLogs, "daemon: sync shared")
	logMu.Lock()
	higherLog := higherLogs.String()
	logMu.Unlock()
	if strings.Contains(higherLog, "daemon: sync shared") {
		t.Fatalf("higher-ID daemon initiated sync: %s", higherLog)
	}

	stopLeft()
	stopRight()
	if err = <-leftDone; err != nil {
		t.Fatal(err)
	}
	if err = <-rightDone; err != nil {
		t.Fatal(err)
	}
}

func waitForDaemonLog(t *testing.T, ctx context.Context, mutex *sync.Mutex, log *strings.Builder, text string) {
	t.Helper()
	for {
		mutex.Lock()
		found := strings.Contains(log.String(), text)
		mutex.Unlock()
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("daemon log did not contain %q", text)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForInitialDaemonSync(t *testing.T, ctx context.Context, mutex *sync.Mutex, logs ...*strings.Builder) {
	t.Helper()
	for {
		mutex.Lock()
		found := false
		for _, log := range logs {
			found = found || strings.Contains(log.String(), "daemon: sync shared")
		}
		mutex.Unlock()
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("initial daemon sync did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForDaemonEndpoints(t *testing.T, ctx context.Context, state *daemonEndpoints) {
	t.Helper()
	for {
		state.RLock()
		ready := state.left != "" && state.right != ""
		state.RUnlock()
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("daemon endpoints were not ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func daemonTestStore(t *testing.T) (*registry.Store, device.Identity) {
	t.Helper()
	directory := t.TempDir()
	store, err := registry.Open(filepath.Join(directory, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	identity, err := device.Load(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	return store, identity
}

func pairDaemonStores(t *testing.T, left *registry.Store, leftIdentity device.Identity, right *registry.Store, rightIdentity device.Identity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type readyPair struct{ code, endpoint string }
	ready := make(chan readyPair, 1)
	serverDone := make(chan error, 1)
	go func() {
		_, err := peernetwork.Pair(ctx, peernetwork.PairOptions{
			Store: left, Identity: leftIdentity, Name: "left", Role: registry.NetworkAdmin,
			ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready:   func(code, endpoint string) { ready <- readyPair{code: code, endpoint: endpoint} },
			Confirm: func(string, string) (bool, error) { return true, nil },
		})
		serverDone <- err
	}()
	pair := <-ready
	if _, err := peernetwork.JoinEndpoint(ctx, pair.endpoint, peernetwork.JoinOptions{
		Store: right, Identity: rightIdentity, Name: "right", Code: pair.code,
		Confirm: func(string, string) (bool, error) { return true, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func daemonNetworkDevice(t *testing.T, store *registry.Store, id string) registry.DeviceRecord {
	t.Helper()
	state, err := store.Network(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range state.Devices {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("network device %s not found", id)
	return registry.DeviceRecord{}
}

func waitForDaemonSync(t *testing.T, ctx context.Context, stores ...*registry.Store) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		converged := true
		for _, store := range stores {
			workspace, err := store.LoadByName(ctx, "shared")
			if err != nil || workspace.State.Aliases["autosynced"] != "workspace" {
				converged = false
				break
			}
		}
		if converged {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("workspace registries did not converge")
		case <-ticker.C:
		}
	}
}
