package network

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/registry"
)

func TestWorkspaceFetchAndBidirectionalSync(t *testing.T) {
	previousPageSize := workspaceManifestPageSize.Swap(1)
	t.Cleanup(func() { workspaceManifestPageSize.Store(previousPageSize) })
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
	asahiRoot := filepath.Join(t.TempDir(), "personal")
	if err = ensureDirectory(asahiRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = Attach(ctx, source, asahiStore, asahiIdentity, "asahi", "personal", asahiRoot); err != nil {
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

func TestAttachAbortsImportWhenManifestPagingIsInterrupted(t *testing.T) {
	previousPageSize := workspaceManifestPageSize.Swap(1)
	t.Cleanup(func() { workspaceManifestPageSize.Store(previousPageSize) })
	sourceStore, sourceIdentity, targetStore, targetIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := sourceStore.Create(ctx, "shared", t.TempDir(), &config.Workspace{Meta: config.Meta{Version: 1}, Projects: map[string]config.Project{}, Aliases: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{sourceIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err = sourceStore.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	options := ServeOptions{Store: sourceStore, Identity: sourceIdentity, Name: "source", ListenAddress: "127.0.0.1:0", DisableDiscovery: true}
	self, cert, listener, err := preparePeerServer(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, peerServerTLS(cert, sourceIdentity, options.Name, trustedPeer(sourceStore)))
	serverOutcome := make(chan error, 1)
	go func() {
		connection, acceptErr := tlsListener.Accept()
		if acceptErr == nil {
			_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
			servePeerConnection(sourceStore, self, connection)
			_ = connection.Close()
		}
		_ = listener.Close()
		serverOutcome <- acceptErr
	}()
	sourceDevice := mustNetworkDevice(t, ctx, targetStore, sourceIdentity.ID())
	source := AvailableWorkspace{WorkspaceSummary: registry.WorkspaceSummary{Name: "shared", WorkspaceID: created.WorkspaceID}, DeviceID: sourceDevice.ID, DeviceName: sourceDevice.Name, Endpoint: listener.Addr().String()}
	if _, err = Attach(ctx, source, targetStore, targetIdentity, "target", "shared", t.TempDir()); err == nil {
		t.Fatal("attach succeeded after peer stopped during manifest paging")
	}
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
	}
	path, found := networkTestStorePaths.Load(targetStore)
	if !found {
		t.Fatal("target registry path is unavailable")
	}
	database, err := sql.Open("sqlite", path.(string))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var imports int
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_imports WHERE workspace_id=?`, created.WorkspaceID).Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if imports != 0 {
		t.Fatalf("interrupted attach retained %d imports", imports)
	}
}

func TestLargeHistoryAttachAndBidirectionalRealPeerSync(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	archRoot := t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{"payload": strings.Repeat("x", 4<<20)}}
	created, err := archStore.Create(ctx, "large", archRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{archIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err = archStore.SetAccess(ctx, "large", policy); err != nil {
		t.Fatal(err)
	}
	for index := range 11 {
		if _, err = archStore.Mutate(ctx, archRoot, func(workspace *config.Workspace) error {
			workspace.Aliases["revision"] = string(rune('a' + index))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := archStore.Export(ctx, "large")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= maxPeerMessageBytes {
		t.Fatalf("large history size = %d, want over %d", len(body), maxPeerMessageBytes)
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
	asahiRoot := t.TempDir()
	source := AvailableWorkspace{WorkspaceSummary: registry.WorkspaceSummary{Name: "large", WorkspaceID: created.WorkspaceID}, DeviceID: arch.ID, DeviceName: arch.Name, Endpoint: address}
	attached, err := Attach(ctx, source, asahiStore, asahiIdentity, "asahi", "large", asahiRoot)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Head == "" || attached.State.Aliases["payload"] != state.Aliases["payload"] {
		t.Fatalf("attached workspace = %#v", attached)
	}
	if _, err = asahiStore.Mutate(ctx, asahiRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["revision"] = "asahi"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Sync(ctx, "large", address, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil || result.Status != "pushed" {
		t.Fatalf("large push result=%#v error=%v", result, err)
	}
	if _, err = archStore.Mutate(ctx, archRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["revision"] = "arch"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = Sync(ctx, "large", address, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil || result.Status != "pulled" {
		t.Fatalf("large pull result=%#v error=%v", result, err)
	}
	converged, err := asahiStore.LoadByName(ctx, "large")
	if err != nil {
		t.Fatal(err)
	}
	if converged.State.Aliases["revision"] != "arch" {
		t.Fatalf("converged aliases = %#v", converged.State.Aliases)
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
	if _, err = requestPeer(ctx, address, arch, asahiStore, asahiIdentity, "asahi", peerRequest{Version: 1, Action: "workspace.inventory", WorkspaceID: created.WorkspaceID, Mode: registry.RevisionImportAttach}); err == nil {
		t.Fatal("unauthorized workspace was fetched")
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

func TestWorkspaceSyncRejectsStaleAuthorizedWriterAfterPullingCurrentHistory(t *testing.T) {
	archStore, archIdentity, asahiStore, asahiIdentity := pairedTestStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	demoted := registry.AccessPolicy{Mode: registry.AccessSelected, Roles: map[string]string{archIdentity.ID(): registry.WorkspaceAdmin, asahiIdentity.ID(): registry.WorkspaceReplica}}
	authoritative, err := archStore.SetAccess(ctx, "shared", demoted)
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
	arch := mustNetworkDevice(t, ctx, asahiStore, archIdentity.ID())
	result, err := Sync(ctx, "shared", <-endpoint, arch, asahiStore, asahiIdentity, "asahi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "rejected" {
		t.Fatalf("stale writer sync status = %q", result.Status)
	}
	converged, err := asahiStore.LoadByName(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if converged.Head != authoritative.Head || converged.Epoch != authoritative.Epoch {
		t.Fatalf("demoted peer workspace = %#v, want head=%s epoch=%d", converged, authoritative.Head, authoritative.Epoch)
	}
	cancel()
	if err = <-serverOutcome; err != nil {
		t.Fatal(err)
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
