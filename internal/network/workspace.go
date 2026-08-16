package network

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
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
	case "workspace.fetch":
		return fetchWorkspace(ctx, store, peerID, request.WorkspaceID, response)
	case "workspace.sync":
		return syncWorkspace(ctx, store, peerID, request.Workspace, response)
	default:
		return errors.New("unsupported peer request")
	}
}

func listWorkspaces(ctx context.Context, store *registry.Store, peerID string, response *peerResponse) error {
	workspaces, err := store.ListShared(ctx, peerID)
	response.Workspaces = workspaces
	return err
}

func fetchWorkspace(ctx context.Context, store *registry.Store, peerID, workspaceID string, response *peerResponse) error {
	name, err := store.WorkspaceNameByID(ctx, workspaceID)
	if err != nil {
		return err
	}
	bundle, err := store.ExportFor(ctx, name, peerID)
	if err == nil {
		response.Workspace = &bundle
	}
	return err
}

func syncWorkspace(ctx context.Context, store *registry.Store, peerID string, incoming *registry.Bundle, response *peerResponse) error {
	if incoming == nil {
		return errors.New("workspace sync bundle is required")
	}
	if err := store.ReconcileNetworkAccess(ctx); err != nil {
		return err
	}
	name, err := store.WorkspaceNameByID(ctx, incoming.WorkspaceID)
	if err != nil {
		return err
	}
	before, err := store.LoadByName(ctx, name)
	if err != nil {
		return err
	}
	after, conflicts, err := store.IntegrateFrom(ctx, name, *incoming, peerID)
	if err != nil {
		return err
	}
	response.SyncStatus = acceptedSyncStatus(before.Head, after.Head, incoming.Heads, conflicts)
	bundle, err := store.ExportFor(ctx, name, peerID)
	if err == nil {
		response.Workspace = &bundle
		response.Conflicts = conflicts
	}
	return err
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

func Fetch(ctx context.Context, source AvailableWorkspace, store *registry.Store, identity device.Identity, name string) (registry.Bundle, error) {
	target, err := networkDevice(ctx, store, source.DeviceID)
	if err != nil {
		return registry.Bundle{}, err
	}
	response, err := requestPeer(ctx, source.Endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.fetch", WorkspaceID: source.WorkspaceID})
	if err != nil {
		return registry.Bundle{}, err
	}
	if response.Workspace == nil {
		return registry.Bundle{}, errors.New("peer returned no workspace bundle")
	}
	return *response.Workspace, nil
}

func Sync(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string) (SyncResult, error) {
	before, err := store.LoadByName(ctx, workspaceName)
	if err != nil {
		return SyncResult{}, err
	}
	bundle, err := store.ExportFor(ctx, workspaceName, target.ID)
	if err != nil {
		return SyncResult{}, err
	}
	response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.sync", WorkspaceID: bundle.WorkspaceID, Workspace: &bundle})
	if err != nil {
		return SyncResult{}, err
	}
	if response.Workspace == nil {
		return SyncResult{}, errors.New("peer returned no workspace bundle")
	}
	after, conflicts, err := store.IntegrateFrom(ctx, workspaceName, *response.Workspace, target.ID)
	if err != nil {
		return SyncResult{}, RejectedError{err: err}
	}
	status := completedSyncStatus(before.Head, after.Head, response.SyncStatus, conflicts, response.Conflicts)
	return SyncResult{Workspace: workspaceName, Device: target.Name, Status: status, Head: after.Head, Conflicts: conflicts}, nil
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
	cert, err := certificate(identity, name)
	if err != nil {
		return peerResponse{}, err
	}
	config, err := peerClientTLS(cert, ed25519.PublicKey(target.PublicKey), target.Name)
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
	if err = json.NewEncoder(connection).Encode(request); err != nil {
		return peerResponse{}, UnavailableError{err: err}
	}
	var response peerResponse
	if err = decodeLimited(connection, maxPeerMessageBytes, &response); err != nil {
		return peerResponse{}, UnavailableError{err: err}
	}
	if response.Error != "" {
		return peerResponse{}, RejectedError{err: errors.New(response.Error)}
	}
	if response.Info.DeviceID != target.ID {
		return peerResponse{}, errors.New("peer response identity does not match certificate")
	}
	if _, err = store.MergeNetwork(ctx, response.Network); err != nil {
		return peerResponse{}, RejectedError{err: err}
	}
	return response, nil
}
