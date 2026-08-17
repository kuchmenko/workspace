package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

func TestWorkspaceFetchAndBidirectionalSync(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	archRoot := t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	created, err := archStore.Create(ctx, "personal", archRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{archIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err = archStore.SetAccess(ctx, "personal", policy); err != nil {
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
	address := <-endpoint
	arch := mustNetworkDevice(t, ctx, asahiStore, archIdentity.ID())
	response, err := requestPeer(ctx, address, arch, asahiStore, asahiIdentity, "asahi", peerRequest{Version: 1, Action: "workspace.list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Workspaces) != 1 || response.Workspaces[0].WorkspaceID != created.WorkspaceID || response.Workspaces[0].Role != registry.WorkspaceWriter {
		t.Fatalf("available workspaces = %#v", response.Workspaces)
	}
	source := AvailableWorkspace{WorkspaceSummary: response.Workspaces[0], DeviceID: arch.ID, DeviceName: arch.Name, Endpoint: address}
	bundle, err := Fetch(ctx, source, asahiStore, asahiIdentity, "asahi")
	if err != nil {
		t.Fatal(err)
	}
	asahiRoot := filepath.Join(t.TempDir(), "personal")
	if err = ensureDirectory(asahiRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = asahiStore.AttachFrom(ctx, "personal", asahiRoot, bundle, arch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = asahiStore.Mutate(ctx, asahiRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["from-asahi"] = "yes"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Sync(ctx, "personal", address, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "pushed" {
		t.Fatalf("Asahi push status = %q", result.Status)
	}
	archWorkspace, err := archStore.LoadByName(ctx, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if archWorkspace.State.Aliases["from-asahi"] != "yes" {
		t.Fatalf("Arch aliases = %#v", archWorkspace.State.Aliases)
	}
	if _, err = archStore.Mutate(ctx, archRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["from-arch"] = "yes"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = Sync(ctx, "personal", address, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "pulled" {
		t.Fatalf("Asahi pull status = %q", result.Status)
	}
	asahiWorkspace, err := asahiStore.LoadByName(ctx, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if asahiWorkspace.State.Aliases["from-arch"] != "yes" || asahiWorkspace.Root != asahiRoot || archWorkspace.Root == asahiWorkspace.Root {
		t.Fatalf("Asahi workspace = %#v", asahiWorkspace)
	}
	result, err = Sync(ctx, "personal", address, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("repeated sync result=%#v error=%v", result, err)
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func TestUnauthorizedWorkspaceIsNotListedOrFetched(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := archStore.Create(ctx, "private", t.TempDir(), &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}})
	if err != nil {
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
	address := <-endpoint
	arch := mustNetworkDevice(t, ctx, asahiStore, archIdentity.ID())
	response, err := requestPeer(ctx, address, arch, asahiStore, asahiIdentity, "asahi", peerRequest{Version: 1, Action: "workspace.list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Workspaces) != 0 {
		t.Fatalf("private workspaces listed = %#v", response.Workspaces)
	}
	if _, err = requestPeer(ctx, address, arch, asahiStore, asahiIdentity, "asahi", peerRequest{Version: 1, Action: "workspace.fetch", WorkspaceID: created.WorkspaceID}); err == nil {
		t.Fatal("unauthorized workspace was fetched")
	}
	leaked, err := archStore.Export(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = requestPeer(ctx, address, arch, asahiStore, asahiIdentity, "asahi", peerRequest{Version: 1, Action: "workspace.sync", WorkspaceID: created.WorkspaceID, Workspace: &leaked}); err == nil {
		t.Fatal("unauthorized workspace source was accepted")
	}
	unchanged, err := archStore.LoadByName(ctx, "private")
	if err != nil || unchanged.Head != created.Head {
		t.Fatalf("private workspace changed=%#v error=%v", unchanged, err)
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceSyncReturnsCurrentBundleAfterRejectingStaleAuthorizedWriter(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx := context.Background()
	archRoot, asahiRoot := t.TempDir(), t.TempDir()
	if _, err := archStore.Create(ctx, "shared", archRoot, &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{archIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err := archStore.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	initial, err := archStore.ExportFor(ctx, "shared", asahiIdentity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = asahiStore.AttachFrom(ctx, "shared", asahiRoot, initial, archIdentity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = asahiStore.Mutate(ctx, asahiRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["stale"] = "asahi"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := asahiStore.Export(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	demoted := registry.AccessPolicy{Mode: registry.AccessSelected, Roles: map[string]string{archIdentity.ID(): registry.WorkspaceAdmin, asahiIdentity.ID(): registry.WorkspaceReplica}}
	authoritative, err := archStore.SetAccess(ctx, "shared", demoted)
	if err != nil {
		t.Fatal(err)
	}
	var response peerResponse
	err = syncWorkspace(ctx, archStore, asahiIdentity.ID(), &stale, &response)
	if err != nil {
		t.Fatalf("stale writer rejection prevented response: %v", err)
	}
	if response.SyncStatus != "rejected" {
		t.Fatalf("stale writer status = %q", response.SyncStatus)
	}
	if response.Workspace == nil {
		t.Fatal("stale authorized writer did not receive current bundle")
	}
	if _, _, err = asahiStore.IntegrateFrom(ctx, "shared", *response.Workspace, archIdentity.ID()); err != nil {
		t.Fatalf("demoted peer could not integrate current bundle: %v", err)
	}
	converged, err := asahiStore.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if converged.Head != authoritative.Head || converged.Epoch != authoritative.Epoch {
		t.Fatalf("demoted peer workspace = %#v, want head=%s epoch=%d", converged, authoritative.Head, authoritative.Epoch)
	}
}

func mustNetworkDevice(t *testing.T, ctx context.Context, store *registry.Store, id string) registry.DeviceRecord {
	t.Helper()
	state, err := store.Network(ctx)
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

func ensureDirectory(path string) error {
	return os.MkdirAll(path, 0o755)
}
