package network

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync/atomic"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

var workspaceManifestPageSize atomic.Int64

func init() {
	workspaceManifestPageSize.Store(10000)
}

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
	default:
		return handleWorkspaceImportRequest(ctx, store, peerID, request, response)
	}
}

func handleWorkspaceImportRequest(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	switch request.Action {
	case "workspace.import.begin":
		return beginWorkspaceManifestImport(ctx, store, peerID, request, response)
	case "workspace.import.append":
		return appendWorkspaceManifestImport(ctx, store, peerID, request)
	case "workspace.import.plan":
		return finishWorkspaceManifestImport(ctx, store, peerID, request, response)
	case "workspace.import.missing":
		return workspaceImportMissing(ctx, store, peerID, request, response)
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
	if errors.Is(err, sql.ErrNoRows) {
		response.WorkspaceMissing = true
		return nil
	}
	if err != nil {
		return err
	}
	manifest, err := store.ManifestPageForLimit(ctx, name, peerID, request.After, int(workspaceManifestPageSize.Load()))
	if err != nil {
		return err
	}
	response.Manifest = &manifest
	if request.Manifest != nil {
		return errors.New("workspace inventory must not include local history")
	}
	return nil
}

func beginWorkspaceManifestImport(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	if request.Mode != registry.RevisionImportSync || request.Manifest == nil || request.Manifest.WorkspaceID != request.WorkspaceID {
		return errors.New("workspace manifest import is invalid")
	}
	name, err := store.WorkspaceNameByID(ctx, request.WorkspaceID)
	if err != nil {
		return err
	}
	id, err := store.BeginSyncImportPage(ctx, name, peerID, *request.Manifest)
	if err == nil {
		response.Import = &registry.RevisionImportPlan{ID: id}
	}
	return err
}

func appendWorkspaceManifestImport(ctx context.Context, store *registry.Store, peerID string, request peerRequest) error {
	if request.Mode != registry.RevisionImportSync || request.Manifest == nil {
		return errors.New("workspace manifest import page is invalid")
	}
	return store.AppendRevisionImportManifest(ctx, request.ImportID, peerID, request.WorkspaceID, request.Mode, request.After, *request.Manifest)
}

func finishWorkspaceManifestImport(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	plan, err := store.FinishRevisionImportManifest(ctx, request.ImportID, peerID, request.WorkspaceID, request.Mode)
	if err == nil {
		response.Import = &plan
	}
	return err
}

func workspaceImportMissing(ctx context.Context, store *registry.Store, peerID string, request peerRequest, response *peerResponse) error {
	plan, err := store.RevisionImportMissingPage(ctx, request.ImportID, peerID, request.WorkspaceID, request.Mode, request.ManifestHash, request.After)
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
		response, endpoint, requestErr := requestPeerCandidates(requestCtx, peer.Endpoints, peer.Device, store, identity, name, peerRequest{Version: 1, Action: "workspace.list"})
		cancel()
		if requestErr != nil {
			continue
		}
		for _, workspace := range response.Workspaces {
			available = append(available, AvailableWorkspace{WorkspaceSummary: workspace, DeviceID: peer.Device.ID, DeviceName: peer.Device.Name, Endpoint: endpoint})
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
	if response.WorkspaceMissing {
		return registry.Workspace{}, UnavailableError{err: errors.New("peer has not attached the workspace")}
	}
	if response.Manifest == nil {
		return registry.Workspace{}, errors.New("peer returned no workspace manifest")
	}
	importID, err := store.BeginAttachImportPage(ctx, localName, root, source.DeviceID, *response.Manifest)
	if err != nil {
		return registry.Workspace{}, err
	}
	manifestHash := ""
	complete := false
	defer func() {
		if !complete {
			_ = store.AbortRevisionImport(context.Background(), importID, source.DeviceID, source.WorkspaceID, registry.RevisionImportAttach, manifestHash)
		}
	}()
	plan, err := appendRemoteManifestPages(ctx, source.Endpoint, target, store, identity, deviceName, registry.RevisionImportAttach, importID, *response.Manifest)
	if err != nil {
		return registry.Workspace{}, err
	}
	manifestHash = plan.ManifestHash
	if err = pullPagedRevisionImport(ctx, source.Endpoint, target, store, identity, deviceName, source.WorkspaceID, registry.RevisionImportAttach, plan); err != nil {
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
	workspaceID := before.WorkspaceID
	response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.inventory", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync})
	if err != nil {
		return SyncResult{}, err
	}
	if response.WorkspaceMissing {
		return SyncResult{}, UnavailableError{err: errors.New("peer has not attached the workspace")}
	}
	if response.Manifest == nil {
		return SyncResult{}, errors.New("peer returned incomplete workspace inventory")
	}
	localImportID, err := store.BeginSyncImportPage(ctx, workspaceName, target.ID, *response.Manifest)
	if err != nil {
		return SyncResult{}, err
	}
	localPlan := registry.RevisionImportPlan{ID: localImportID}
	localComplete := false
	defer abortLocalRevisionImportUnlessComplete(store, target.ID, workspaceID, &localPlan, &localComplete)
	localPlan, err = appendRemoteManifestPages(ctx, endpoint, target, store, identity, name, registry.RevisionImportSync, localImportID, *response.Manifest)
	if err != nil {
		return SyncResult{}, err
	}
	remotePlan, err := pushLocalManifestPages(ctx, workspaceName, endpoint, target, store, identity, name, workspaceID)
	remoteComplete := false
	defer abortPeerRevisionImportUnlessComplete(endpoint, target, store, identity, name, workspaceID, &remotePlan, &remoteComplete)
	if err != nil {
		return SyncResult{}, err
	}
	finished, after, conflicts, err := finishPagedSync(ctx, workspaceName, endpoint, target, store, identity, name, workspaceID, localPlan, remotePlan)
	if err != nil {
		return SyncResult{}, err
	}
	localComplete = true
	remoteComplete = true
	status := completedSyncStatus(before.Head, after.Head, finished.SyncStatus, conflicts, finished.Conflicts)
	return SyncResult{Workspace: workspaceName, Device: target.Name, Status: status, Head: after.Head, Conflicts: conflicts}, nil
}

func finishPagedSync(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string, localPlan, remotePlan registry.RevisionImportPlan) (peerResponse, registry.Workspace, []registry.Conflict, error) {
	finished, after, conflicts, err := exchangePagedRevisionImports(ctx, workspaceName, endpoint, target, store, identity, name, workspaceID, localPlan, remotePlan)
	if !errors.Is(err, registry.ErrWorkspaceAccessConflict) {
		return finished, after, conflicts, err
	}
	after, err = store.LoadByName(ctx, workspaceName)
	if err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	finished.SyncStatus = "conflicted"
	return finished, after, conflicts, nil
}

func appendRemoteManifestPages(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, mode, importID string, first registry.RevisionManifest) (registry.RevisionImportPlan, error) {
	page := first
	plan := registry.RevisionImportPlan{ID: importID}
	for page.Next != "" {
		after := page.Next
		response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.inventory", WorkspaceID: page.WorkspaceID, Mode: mode, After: after})
		if err != nil {
			return plan, err
		}
		if response.Manifest == nil {
			return plan, errors.New("peer returned no workspace manifest page")
		}
		page = *response.Manifest
		if err = store.AppendRevisionImportManifest(ctx, importID, target.ID, page.WorkspaceID, mode, after, page); err != nil {
			return plan, err
		}
	}
	return store.FinishRevisionImportManifest(ctx, importID, target.ID, first.WorkspaceID, mode)
}

func pushLocalManifestPages(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string) (registry.RevisionImportPlan, error) {
	page, err := store.ManifestPageForLimit(ctx, workspaceName, target.ID, "", int(workspaceManifestPageSize.Load()))
	if err != nil {
		return registry.RevisionImportPlan{}, err
	}
	response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.begin", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, Manifest: &page})
	if err != nil || response.Import == nil {
		return registry.RevisionImportPlan{}, errors.Join(err, errors.New("peer returned no workspace manifest import"))
	}
	importID := response.Import.ID
	plan := registry.RevisionImportPlan{ID: importID}
	for page.Next != "" {
		after := page.Next
		page, err = store.ManifestPageForLimit(ctx, workspaceName, target.ID, after, int(workspaceManifestPageSize.Load()))
		if err != nil {
			return plan, err
		}
		if _, err = requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.append", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: importID, After: after, Manifest: &page}); err != nil {
			return plan, err
		}
	}
	response, err = requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.plan", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: importID})
	if err != nil || response.Import == nil {
		return plan, errors.Join(err, errors.New("peer returned no workspace import plan"))
	}
	return *response.Import, nil
}

func exchangePagedRevisionImports(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string, localPlan, remotePlan registry.RevisionImportPlan) (peerResponse, registry.Workspace, []registry.Conflict, error) {
	if err := pullPagedRevisionImport(ctx, endpoint, target, store, identity, name, workspaceID, registry.RevisionImportSync, localPlan); err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	if err := pushPagedRevisionImport(ctx, workspaceName, endpoint, target, store, identity, name, workspaceID, remotePlan); err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	finished, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.finish", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: remotePlan.ID, ManifestHash: remotePlan.ManifestHash})
	if err != nil {
		return peerResponse{}, registry.Workspace{}, nil, err
	}
	after, conflicts, _, err := store.FinishSyncImport(ctx, localPlan.ID, target.ID, workspaceID, localPlan.ManifestHash)
	return finished, after, conflicts, err
}

func pullPagedRevisionImport(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID, mode string, plan registry.RevisionImportPlan) error {
	for {
		if err := pullRevisionImportPage(ctx, endpoint, target, store, identity, name, workspaceID, mode, plan); err != nil {
			return err
		}
		if plan.Next == "" {
			return nil
		}
		var err error
		plan, err = store.RevisionImportMissingPage(ctx, plan.ID, target.ID, workspaceID, mode, plan.ManifestHash, plan.Next)
		if err != nil {
			return err
		}
	}
}

func pullRevisionImportPage(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID, mode string, plan registry.RevisionImportPlan) error {
	if len(plan.Missing) == 0 {
		return nil
	}
	response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.revisions", WorkspaceID: workspaceID, RevisionIDs: plan.Missing})
	if err != nil {
		return err
	}
	if !revisionBatchMatches(plan.Missing, response.Revisions) {
		return errors.New("peer returned a mismatched revision batch")
	}
	return store.StageRevisionImport(ctx, plan.ID, target.ID, workspaceID, mode, plan.ManifestHash, response.Revisions)
}

func pushPagedRevisionImport(ctx context.Context, workspaceName, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string, plan registry.RevisionImportPlan) error {
	for {
		if len(plan.Missing) > 0 {
			revisions, err := store.RevisionsFor(ctx, workspaceName, target.ID, plan.Missing)
			if err != nil {
				return err
			}
			if _, err = requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.batch", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: plan.ID, ManifestHash: plan.ManifestHash, Revisions: revisions}); err != nil {
				return err
			}
		}
		if plan.Next == "" {
			return nil
		}
		response, err := requestPeer(ctx, endpoint, target, store, identity, name, peerRequest{Version: 1, Action: "workspace.import.missing", WorkspaceID: workspaceID, Mode: registry.RevisionImportSync, ImportID: plan.ID, ManifestHash: plan.ManifestHash, After: plan.Next})
		if err != nil || response.Import == nil {
			return errors.Join(err, errors.New("peer returned no workspace import plan page"))
		}
		plan = *response.Import
	}
}

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

func abortLocalRevisionImportUnlessComplete(store *registry.Store, peerID, workspaceID string, plan *registry.RevisionImportPlan, complete *bool) {
	if !*complete {
		_ = store.AbortRevisionImport(context.Background(), plan.ID, peerID, workspaceID, registry.RevisionImportSync, plan.ManifestHash)
	}
}

func abortPeerRevisionImportUnlessComplete(endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name, workspaceID string, plan *registry.RevisionImportPlan, complete *bool) {
	if !*complete && plan.ID != "" {
		abortPeerRevisionImport(endpoint, target, store, identity, name, workspaceID, *plan)
	}
}

func completedSyncStatus(before, after, remoteStatus string, localConflicts, remoteConflicts []registry.Conflict) string {
	if remoteStatus == "rejected" {
		return "rejected"
	}
	if remoteStatus == "conflicted" || len(localConflicts) > 0 || len(remoteConflicts) > 0 {
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
	Device    registry.DeviceRecord
	Endpoints []string
}

func DiscoverPeers(ctx context.Context, store *registry.Store, identity device.Identity, name string, discoveryWindow time.Duration) ([]PeerEndpoint, error) {
	peers, err := discoverActivePeers(ctx, store, identity.ID(), discoveryWindow)
	if err != nil {
		return nil, err
	}
	result := make([]PeerEndpoint, 0, len(peers))
	for _, peer := range peers {
		probeContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, endpoint, probeErr := probeCandidates(probeContext, peer.Endpoints, peer.Device, store, identity, name)
		cancel()
		if probeErr == nil {
			result = append(result, PeerEndpoint{Device: peer.Device, Endpoint: endpoint})
		}
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
		if record.ID != localID && record.Active && len(endpoints[record.ID]) > 0 {
			peers = append(peers, discoveredPeer{Device: record, Endpoints: endpoints[record.ID]})
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
	response, _, err := requestPeerCandidates(ctx, []string{endpoint}, target, store, identity, name, request)
	return response, err
}

func requestPeerCandidates(ctx context.Context, endpoints []string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string, request peerRequest) (peerResponse, string, error) {
	cert, err := peerCertificate(identity, name)
	if err != nil {
		return peerResponse{}, "", err
	}
	config, err := peerClientTLS(cert, ed25519.PublicKey(target.PublicKey))
	if err != nil {
		return peerResponse{}, "", err
	}
	dialContext, cancel := context.WithTimeout(ctx, peerExchangeTimeout)
	connection, endpoint, err := dialPeerCandidates(dialContext, endpoints, config)
	cancel()
	if err != nil {
		return peerResponse{}, "", UnavailableError{err: err}
	}
	defer func() { _ = connection.Close() }()
	stop := watchConnection(ctx, connection)
	defer stop()
	response, err := exchangePeer(ctx, connection, target, store, request)
	return response, endpoint, err
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
		return peerResponse{}, RejectedError{err: errors.New(safePeerText(response.Error))}
	}
	if errors.Is(err, registry.ErrNetworkConflict) {
		return peerResponse{}, RejectedError{err: err}
	}
	return response, nil
}
