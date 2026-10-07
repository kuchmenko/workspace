package daemon

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/daemonipc"
	"github.com/kuchmenko/workspace/internal/device"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
)

const (
	DefaultSyncInterval    = time.Minute
	DefaultDiscoveryWindow = 1500 * time.Millisecond
	defaultDebounce        = 250 * time.Millisecond
)

type Options struct {
	Store            *registry.Store
	Identity         device.Identity
	Name             string
	ListenAddress    string
	DisableDiscovery bool
	SyncInterval     time.Duration
	GitSyncInterval  time.Duration
	DiscoveryWindow  time.Duration
	Ready            func(endpoint string)
	Logf             func(format string, args ...any)
	Discover         func(context.Context) ([]peernetwork.PeerEndpoint, error)
}

func Run(ctx context.Context, options Options) error {
	if options.Store == nil {
		return errors.New("daemon store is required")
	}
	if options.Name == "" {
		return errors.New("daemon device name is required")
	}
	if options.SyncInterval <= 0 {
		options.SyncInterval = DefaultSyncInterval
	}
	if options.DiscoveryWindow <= 0 {
		options.DiscoveryWindow = DefaultDiscoveryWindow
	}
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	if options.Discover == nil {
		options.Discover = func(discoveryContext context.Context) ([]peernetwork.PeerEndpoint, error) {
			return peernetwork.DiscoverPeers(discoveryContext, options.Store, options.Identity, options.Name, options.DiscoveryWindow)
		}
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	materializer := newMaterializer(options)
	scheduler := newScheduler(options, materializer)
	localServer, err := daemonipc.Listen(options.Store.Path())
	if err != nil {
		return err
	}
	defer func() { _ = localServer.Close() }()
	localDone := make(chan error, 1)
	go func() { localDone <- localServer.Serve(runContext, scheduler.trigger) }()
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		scheduler.run(runContext)
	}()
	materializerDone := make(chan struct{})
	go func() {
		defer close(materializerDone)
		materializer.run(runContext)
	}()
	materializer.triggerAll()

	err = peernetwork.Serve(runContext, peernetwork.ServeOptions{
		Store:            options.Store,
		Identity:         options.Identity,
		Name:             options.Name,
		ListenAddress:    options.ListenAddress,
		DisableDiscovery: options.DisableDiscovery,
		Ready: func(endpoint string) {
			scheduler.triggerAll()
			if options.Ready != nil {
				options.Ready(endpoint)
			}
		},
		WorkspaceWake: scheduler.wake,
		WorkspaceChanged: func(workspaceID string, projects []string) {
			for _, project := range projects {
				materializer.trigger(workspaceID, project)
			}
		},
	})
	cancel()
	<-schedulerDone
	<-materializerDone
	return errors.Join(err, <-localDone)
}

type scheduler struct {
	options        Options
	git            *materializer
	triggers       chan string
	endpoints      map[string]string
	workspacesSeen map[string]registry.Workspace
}

func newScheduler(options Options, materializer *materializer) *scheduler {
	return &scheduler{options: options, git: materializer, triggers: make(chan string, 128), endpoints: map[string]string{}, workspacesSeen: map[string]registry.Workspace{}}
}

func (scheduler *scheduler) wake(peerID, workspaceID string) {
	if scheduler.options.Identity.ID() >= peerID {
		return
	}
	scheduler.trigger(workspaceID)
}

func (scheduler *scheduler) trigger(workspaceID string) {
	select {
	case scheduler.triggers <- workspaceID:
	default:
	}
}

func (scheduler *scheduler) triggerAll() {
	scheduler.trigger("")
}

func (scheduler *scheduler) run(ctx context.Context) {
	periodic := time.NewTicker(scheduler.options.SyncInterval)
	defer periodic.Stop()
	var debounce *time.Timer
	var ready <-chan time.Time
	pending := map[string]bool{}
	all := false
	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return
		case <-periodic.C:
			all = true
			debounce, ready = armPendingTimer(debounce, ready)
		case workspaceID := <-scheduler.triggers:
			all = addPendingWorkspace(workspaceID, all, pending)
			debounce, ready = armPendingTimer(debounce, ready)
		case <-ready:
			scheduler.sync(ctx, all, pending)
			all = false
			pending = map[string]bool{}
			ready = nil
		}
	}
}

func addPendingWorkspace(workspaceID string, all bool, pending map[string]bool) bool {
	if workspaceID == "" {
		return true
	}
	pending[workspaceID] = true
	return all
}

func armPendingTimer(timer *time.Timer, ready <-chan time.Time) (*time.Timer, <-chan time.Time) {
	if ready != nil {
		return timer, ready
	}
	return armTimer(timer, defaultDebounce)
}

func armTimer(timer *time.Timer, delay time.Duration) (*time.Timer, <-chan time.Time) {
	if timer == nil {
		timer = time.NewTimer(delay)
		return timer, timer.C
	}
	timer.Reset(delay)
	return timer, timer.C
}

func (scheduler *scheduler) sync(ctx context.Context, all bool, pending map[string]bool) {
	workspaces, err := scheduler.workspaces(ctx, all, pending)
	if err != nil {
		scheduler.options.Logf("daemon: load workspaces: %v", err)
		return
	}
	if len(workspaces) == 0 {
		return
	}
	for _, workspace := range workspaces {
		scheduler.scheduleGitChanges(workspace)
	}
	if all || len(scheduler.endpoints) == 0 {
		peers, err := scheduler.options.Discover(ctx)
		if err != nil {
			scheduler.options.Logf("daemon: discover peers: %v", err)
			return
		}
		scheduler.endpoints = make(map[string]string, len(peers))
		for _, peer := range peers {
			scheduler.endpoints[peer.Device.ID] = peer.Endpoint
		}
	}
	state, err := scheduler.options.Store.Network(ctx)
	if err != nil {
		scheduler.options.Logf("daemon: load network: %v", err)
		return
	}
	devices := append([]registry.DeviceRecord(nil), state.Devices...)
	sort.Slice(devices, func(left, right int) bool { return devices[left].ID < devices[right].ID })
	for _, workspace := range workspaces {
		scheduler.syncWorkspace(ctx, workspace, devices, scheduler.endpoints)
	}
}

func (scheduler *scheduler) scheduleGitChanges(workspace registry.Workspace) {
	previous := scheduler.workspacesSeen[workspace.WorkspaceID]
	for name, project := range workspace.State.Projects {
		if previous.State == nil || !reflect.DeepEqual(previous.State.Projects[name], project) {
			scheduler.git.trigger(workspace.WorkspaceID, name)
		}
	}
	scheduler.workspacesSeen[workspace.WorkspaceID] = workspace
}

func (scheduler *scheduler) workspaces(ctx context.Context, all bool, pending map[string]bool) ([]registry.Workspace, error) {
	if all {
		return scheduler.options.Store.List(ctx)
	}
	workspaces := make([]registry.Workspace, 0, len(pending))
	for workspaceID := range pending {
		name, err := scheduler.options.Store.WorkspaceNameByID(ctx, workspaceID)
		if err != nil {
			continue
		}
		workspace, err := scheduler.options.Store.LoadByName(ctx, name)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].Name < workspaces[right].Name })
	return workspaces, nil
}

func (scheduler *scheduler) syncWorkspace(ctx context.Context, workspace registry.Workspace, devices []registry.DeviceRecord, endpoints map[string]string) {
	localID := scheduler.options.Identity.ID()
	for _, peer := range devices {
		if err := ctx.Err(); err != nil {
			return
		}
		if peer.ID == localID || !peer.Active || endpoints[peer.ID] == "" {
			continue
		}
		if _, err := scheduler.options.Store.ManifestFor(ctx, workspace.Name, peer.ID); err != nil {
			continue
		}
		if localID < peer.ID {
			scheduler.exchangeWorkspace(ctx, workspace, peer, endpoints[peer.ID])
			continue
		}
		if err := peernetwork.WakeWorkspace(ctx, workspace.WorkspaceID, endpoints[peer.ID], peer, scheduler.options.Store, scheduler.options.Identity, scheduler.options.Name); err != nil {
			scheduler.options.Logf("daemon: wake %s for %s: %v", peer.Name, workspace.Name, err)
		}
	}
}

func (scheduler *scheduler) exchangeWorkspace(ctx context.Context, workspace registry.Workspace, peer registry.DeviceRecord, endpoint string) {
	result, err := peernetwork.Sync(ctx, workspace.Name, endpoint, peer, scheduler.options.Store, scheduler.options.Identity, scheduler.options.Name)
	if err != nil {
		scheduler.options.Logf("daemon: sync %s with %s: %v", workspace.Name, peer.Name, err)
		return
	}
	scheduler.options.Logf("daemon: sync %s with %s: %s", workspace.Name, peer.Name, result.Status)
	if result.Head != workspace.Head {
		if updated, loadErr := scheduler.options.Store.LoadByName(ctx, workspace.Name); loadErr == nil {
			scheduler.scheduleGitChanges(updated)
		}
		scheduler.trigger(workspace.WorkspaceID)
	}
}
