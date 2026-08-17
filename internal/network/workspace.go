package network

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

type AvailableWorkspace struct {
	registry.WorkspaceSummary
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Endpoint   string `json:"endpoint"`
}

type SyncResult struct {
	Workspace string              `json:"workspace"`
	Device    string              `json:"device"`
	Status    string              `json:"status"`
	Head      string              `json:"head"`
	Conflicts []registry.Conflict `json:"conflicts,omitempty"`
}

type PeerEndpoint struct {
	Device   registry.DeviceRecord `json:"device"`
	Endpoint string                `json:"endpoint"`
}

func handleWorkspaceRequest(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	switch request.Action {
	case "status":
		return nil
	case "workspace.list":
		return listWorkspaces(ctx, store, peerID, response)
	case "workspace.inventory":
		return inventoryWorkspace(ctx, store, peerID, request, response)
	case "workspace.revisions":
		return workspaceRevisions(ctx, store, peerID, request, response)
	case "workspace.import.batch":
		return stageWorkspaceImport(ctx, store, peerID, request)
	case "workspace.import.finish":
		return finishWorkspaceImport(ctx, store, peerID, request, response)
	case "workspace.import.abort":
		return store.AbortRevisionImport(ctx, request.ImportID, peerID, request.WorkspaceID, request.Mode, request.ManifestHash)
	default:
		return errors.New("unsupported peer request")
	}
}

func listWorkspaces(ctx context.Context, store *registry.Store, peerID string, response *peerResponse) error {
	workspaces, err := store.ListShared(ctx, peerID)
	response.Workspaces = workspaces
	return err
}

func inventoryWorkspace(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	if request.Mode != registry.RevisionImportAttach && request.Mode != registry.RevisionImportSync {
		return errors.New("workspace inventory mode is invalid")
	}
	name, err := store.WorkspaceNameByID(ctx, request.WorkspaceID)
	if err != nil {
		return err
	}
	manifest, err := store.ManifestFor(ctx, name, peerID)
	if err != nil {
		return err
	}
	response.Manifest = &manifest
	if request.Mode == registry.RevisionImportAttach {
		if request.Manifest != nil {
			return errors.New("workspace attach inventory must not include local history")
		}
		return nil
	}
	if request.Manifest == nil || request.Manifest.WorkspaceID != request.WorkspaceID {
		return errors.New("workspace sync inventory is required")
	}
	plan, err := store.BeginSyncImport(ctx, name, peerID, *request.Manifest)
	if err == nil {
		response.Import = &plan
	}
	return err
}

func workspaceRevisions(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	name, err := store.WorkspaceNameByID(ctx, request.WorkspaceID)
	if err != nil {
		return err
	}
	response.Revisions, err = store.RevisionsFor(ctx, name, peerID, request.RevisionIDs)
	return err
}

func stageWorkspaceImport(ctx context.Context, store *registry.Store, peerID string, request peerRequest) error {
	if request.Mode != registry.RevisionImportSync {
		return errors.New("peer can stage only workspace sync imports")
	}
	return store.StageRevisionImport(ctx, request.ImportID, peerID, request.WorkspaceID, request.Mode, request.ManifestHash, request.Revisions)
}

func finishWorkspaceImport(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	if request.Mode != registry.RevisionImportSync {
		return errors.New("peer can finish only workspace sync imports")
	}
	name, err := store.WorkspaceNameByID(ctx, request.WorkspaceID)
	if err != nil {
		return err
	}
	before, err := store.LoadByName(ctx, name)
	if err != nil {
		return err
	}
	after, conflicts, heads, err := store.FinishSyncImport(ctx, request.ImportID, peerID, request.WorkspaceID, request.ManifestHash)
	if err != nil {
		switch {
		case errors.Is(err, registry.ErrWorkspaceEpochStale):
			response.SyncStatus = "rejected"
		case errors.Is(err, registry.ErrWorkspaceAccessConflict):
			response.SyncStatus = "conflicted"
		default:
			return err
		}
	} else {
		response.SyncStatus = acceptedSyncStatus(before.Head, after.Head, heads, conflicts)
	}
	response.Conflicts = conflicts
	return nil
}

func acceptedSyncStatus(before, after string, incomingHeads []string, conflicts []registry.Conflict) string {
	if len(conflicts) > 0 {
		return "conflicted"
	}
	if after == before {
		return "unchanged"
	}
	for _, head := range incomingHeads {
		if after == head {
			return "pulled"
		}
	}
	return "merged"
}

func Available(ctx context.Context, store *registry.Store, identity device.Identity, name string, discoveryWindow time.Duration) ([]AvailableWorkspace, error) {
	peers, err := discoverActivePeers(ctx, store, identity.ID(), discoveryWindow)
	if err != nil {
		return nil, err
	}
	var available []AvailableWorkspace
	for _, peer := range peers {
		requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		response, requestErr := requestPeer(requestCtx, peer.Endpoint, peer.Device, store, identity, name, peerRequest{Version: 1, Action: "workspace.list"})
		cancel()
		if requestErr != nil {
			continue
		}
		for _, workspace := range response.Workspaces {
			available = append(available, AvailableWorkspace{WorkspaceSummary: workspace, DeviceID: peer.Device.ID, DeviceName: peer.Device.Name, Endpoint: peer.Endpoint})
		}
	}
	sort.Slice(available, func(left, right int) bool {
		if available[left].Name != available[right].Name {
			return available[left].Name < available[right].Name
		}
		return available[left].DeviceName < available[right].DeviceName
	})
	return available, nil
}

func Attach(ctx context.Context, source AvailableWorkspace, store *registry.Store, identity device.Identity, deviceName, localName, root string) (registry.Workspace, error) {
	target, err := networkDevice(ctx, store, source.DeviceID)
	if err != nil {
		return registry.Workspace{}, err
	}
	response, err := requestPeer(ctx, source.Endpoint, target, store, identity, deviceName, peerRequest{Version: 1, Action: "workspace.inventory", WorkspaceID: source.WorkspaceID, Mode: registry.RevisionImportAttach})
	if err != nil {
		return registry.Workspace{}, err
	}
	if response.Manifest == nil {
		return registry.Workspace{}, errors.New("peer returned no workspace manifest")
	}
	plan, err := store.BeginAttachImport(ctx, localName, root, source.DeviceID, *response.Manifest)
	if err != nil {
		return registry.Workspace{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = store.AbortRevisionImport(context.Background(), plan.ID, source.DeviceID, source.WorkspaceID, registry.RevisionImportAttach, plan.ManifestHash)
		}
	}()
	if err = pullRevisionImport(ctx, source.Endpoint, target, store, identity, deviceName, *response.Manifest, plan, registry.RevisionImportAttach); err != nil {
		return registry.Workspace{}, err
	}
	workspace, err := store.FinishAttachImport(ctx, plan.ID, source.DeviceID, source.WorkspaceID, plan.ManifestHash)
	if err != nil {
		return registry.Workspace{}, err
	}
	complete = true
	return workspace, nil
}

func Sync(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string) (SyncResult, error) {
	before, err := store.LoadByName(ctx, workspaceName)
	if err != nil {
		return SyncResult{}, err
	}
	localManifest, err := store.ManifestFor(ctx, workspaceName, target.ID)
	if err != nil {
		return SyncResult{}, err
	}
	response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.inventory", WorkspaceID: localManifest.WorkspaceID, Mode: registry.RevisionImportSync, Manifest: &localManifest})
	if err != nil {
		return SyncResult{}, err
	}
	if response.Manifest == nil || response.Import == nil {
		return SyncResult{}, errors.New("peer returned incomplete workspace inventory")
	}
	remoteManifest, remotePlan := *response.Manifest, *response.Import
	localPlan, err := store.BeginSyncImport(ctx, workspaceName, target.ID, remoteManifest)
	if err != nil {
		abortPeerRevisionImport(endpoint, target, store, identity, name, localManifest.WorkspaceID, remotePlan)
		return SyncResult{}, err
	}
	finished, after, conflicts, err := exchangeRevisionImports(ctx, workspaceName, endpoint, target, store, identity, name, localManifest, remoteManifest, localPlan, remotePlan)
	if errors.Is(err, registry.ErrWorkspaceAccessConflict) {
		after, err = store.LoadByName(ctx, workspaceName)
		if err == nil {
			return SyncResult{Workspace: workspaceName, Device: target.Name, Status: "conflicted", Head: after.Head}, nil
		}
	}
	if err != nil {
		return SyncResult{}, err
	}
	status := completedSyncStatus(before.Head, after.Head, finished.SyncStatus, conflicts, finished.Conflicts)
	return SyncResult{Workspace: workspaceName, Device: target.Name, Status: status, Head: after.Head, Conflicts: conflicts}, nil
}

func exchangeRevisionImports(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string, localManifest, remoteManifest registry.RevisionManifest, localPlan, remotePlan registry.RevisionImportPlan) (peerResponse, registry.Workspace, []registry.Conflict, error) {
	localComplete, remoteComplete := false, false
	defer func() {
		if !localComplete {
			_ = store.AbortRevisionImport(context.Background(), localPlan.ID, target.ID, localManifest.WorkspaceID, registry.RevisionImportSync, localPlan.ManifestHash)
		}
		if !remoteComplete {
			abortPeerRevisionImport(endpoint, target, store, identity, name, localManifest.WorkspaceID, remotePlan)
		}
	}()
	if err := pullRevisionImport(ctx, endpoint, target, store, identity, name, remoteManifest, localPlan, registry.RevisionImportSync); err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	if err := pushRevisionImport(ctx, workspaceName, endpoint, target, store, identity, name, localManifest, remotePlan); err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	finished, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.finish", WorkspaceID: localManifest.WorkspaceID, Mode: registry.RevisionImportSync, ImportID: remotePlan.ID, ManifestHash: remotePlan.ManifestHash})
	if err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	remoteComplete = true
	after, conflicts, _, err := store.FinishSyncImport(ctx, localPlan.ID, target.ID, localManifest.WorkspaceID, localPlan.ManifestHash)
	if errors.Is(err, registry.ErrWorkspaceEpochStale) {
		after, err = store.LoadByName(ctx, workspaceName)
	}
	if err != nil {
		return peerResponse{}, registry.Workspace{}, nil, RejectedError{err: err}
	}
	localComplete = true
	return finished, after, conflicts, nil
}

const maxPeerRevisionBatchBytes = 16 << 20

func pullRevisionImport(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string, manifest registry.RevisionManifest, plan registry.RevisionImportPlan, mode string) error {
	batches, err := revisionBatches(manifest, plan.Missing)
	if err != nil {
		return err
	}
	for _, ids := range batches {
		response, requestErr := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.revisions", WorkspaceID: manifest.WorkspaceID, RevisionIDs: ids})
		if requestErr != nil {
			return requestErr
		}
		if !revisionBatchMatches(ids, response.Revisions) {
			return errors.New("peer returned a mismatched revision batch")
		}
		if requestErr = store.StageRevisionImport(ctx, plan.ID, target.ID, manifest.WorkspaceID, mode, plan.ManifestHash, response.Revisions); requestErr != nil {
			return requestErr
		}
	}
	return nil
}

func pushRevisionImport(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string, manifest registry.RevisionManifest, plan registry.RevisionImportPlan) error {
	batches, err := revisionBatches(manifest, plan.Missing)
	if err != nil {
		return err
	}
	for _, ids := range batches {
		revisions, loadErr := store.RevisionsFor(ctx, workspaceName, target.ID, ids)
		if loadErr != nil {
			return loadErr
		}
		_, requestErr := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.batch", WorkspaceID: manifest.WorkspaceID, Mode: registry.RevisionImportSync, ImportID: plan.ID, ManifestHash: plan.ManifestHash, Revisions: revisions})
		if requestErr != nil {
			return requestErr
		}
	}
	return nil
}

func revisionBatches(manifest registry.RevisionManifest, missing []string) ([][]string, error) {
	sizes := make(map[string]int64, len(manifest.Revisions))
	for _, revision := range manifest.Revisions {
		sizes[revision.ID] = revision.WireBytes
	}
	seen := make(map[string]bool, len(missing))
	var batches [][]string
	var batch []string
	var bytes int64
	for _, id := range missing {
		size, declared := sizes[id]
		if !declared || seen[id] || size < 1 || size >= maxPeerMessageBytes {
			return nil, errors.New("revision import plan is invalid")
		}
		seen[id] = true
		if len(batch) > 0 && (bytes+size >= maxPeerRevisionBatchBytes || len(batch) == maxImportBatchRevisions) {
			batches = append(batches, batch)
			batch, bytes = nil, 0
		}
		batch = append(batch, id)
		bytes += size
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches, nil
}

const maxImportBatchRevisions = 128

func revisionBatchMatches(ids []string, revisions []registry.Revision) bool {
	if len(ids) != len(revisions) {
		return false
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	for _, revision := range revisions {
		if !wanted[revision.ID] {
			return false
		}
		delete(wanted, revision.ID)
	}
	return len(wanted) == 0
}

func abortPeerRevisionImport(endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string, plan registry.RevisionImportPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.abort", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: plan.ID, ManifestHash: plan.ManifestHash})
}

func completedSyncStatus(before, after, remoteStatus string, localConflicts, remoteConflicts []registry.Conflict) string {
	if len(localConflicts) > 0 || len(remoteConflicts) > 0 {
		return "conflicted"
	}
	if remoteStatus == "merged" {
		return "merged"
	}
	if after != before {
		return "pulled"
	}
	if remoteStatus == "pulled" {
		return "pushed"
	}
	return "unchanged"
}

type discoveredPeer struct {
	Device   registry.DeviceRecord
	Endpoint string
}

func DiscoverPeers(ctx context.Context, store *registry.Store, localID string, discoveryWindow time.Duration) ([]PeerEndpoint, error) {
	peers, err := discoverActivePeers(ctx, store, localID, discoveryWindow)
	if err != nil {
		return nil, err
	}
	result := make([]PeerEndpoint, len(peers))
	for index, peer := range peers {
		result[index].Device, result[index].Endpoint = peer.Device, peer.Endpoint
	}
	return result, nil
}

func discoverActivePeers(ctx context.Context, store *registry.Store, localID string, discoveryWindow time.Duration) ([]discoveredPeer, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return nil, err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, discoveryWindow)
	endpoints, err := discoverPeers(discoveryCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	var peers []discoveredPeer
	for _, record := range state.Devices {
		if record.ID != localID && record.Active && endpoints[record.ID] != "" {
			peers = append(peers, discoveredPeer{Device: record, Endpoint: endpoints[record.ID]})
		}
	}
	sort.Slice(peers, func(left, right int) bool { return peers[left].Device.Name < peers[right].Device.Name })
	return peers, nil
}

func networkDevice(ctx context.Context, store *registry.Store, id string) (registry.DeviceRecord, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return registry.DeviceRecord{}, err
	}
	for _, record := range state.Devices {
		if record.ID == id && record.Active {
			return record, nil
		}
	}
	return registry.DeviceRecord{}, fmt.Errorf("network device %s is not active", id)
}

func requestPeer(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string, request peerRequest) (peerResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, peerExchangeTimeout)
	defer cancel()
	cert, err := peerCertificate(identity, name)
	if err != nil {
		return peerResponse{}, err
	}
	config, err := peerClientTLS(cert, ed25519.PublicKey(target.PublicKey))
	if err != nil {
		return peerResponse{}, err
	}
	dialer := tls.Dialer{Config: config}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return peerResponse{}, UnavailableError{err: err}
	}
	defer func() { _ = connection.Close() }()
	stop := watchConnection(ctx, connection)
	defer stop()
	return exchangePeer(ctx, connection, target, store, request)
}

func exchangePeer(ctx context.Context, connection net.Conn, target registry.DeviceRecord, store *registry.Store, request peerRequest) (peerResponse, error) {
	var err error
	request.Network, err = store.ExportNetwork(ctx)
	if err != nil {
		return peerResponse{}, err
	}
	if err = encodePeerFrame(connection, request); err != nil {
		return peerResponse{}, UnavailableError{err: err}
	}
	var response peerResponse
	if err = decodePeerFrame(connection, &response); err != nil {
		return peerResponse{}, UnavailableError{err: err}
	}
	if response.Info.DeviceID != target.ID {
		return peerResponse{}, errors.New("peer response identity does not match certificate")
	}
	if _, err = store.MergeNetworkFrom(ctx, response.Network, target.ID); err != nil && !errors.Is(err, registry.ErrNetworkConflict) {
		return peerResponse{}, RejectedError{err: err}
	}
	if response.Error != "" {
		return peerResponse{}, RejectedError{err: errors.New(response.Error)}
	}
	if errors.Is(err, registry.ErrNetworkConflict) {
		return peerResponse{}, RejectedError{err: err}
	}
	return response, nil
}
