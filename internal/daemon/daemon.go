package daemon

import (
	"context"
	"errors"
	"sort"
	"time"

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
	scheduler := newScheduler(options)
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		scheduler.run(runContext)
	}()

	err := peernetwork.Serve(runContext, peernetwork.ServeOptions{
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
	})
	cancel()
	<-schedulerDone
	return err
}

type scheduler struct {
	options  Options
	triggers chan string
}

func newScheduler(options Options) *scheduler {
	return &scheduler{options: options, triggers: make(chan string, 128)}
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
	peers, err := scheduler.options.Discover(ctx)
	if err != nil {
		scheduler.options.Logf("daemon: discover peers: %v", err)
		return
	}
	endpoints := make(map[string]string, len(peers))
	for _, peer := range peers {
		endpoints[peer.Device.ID] = peer.Endpoint
	}
	state, err := scheduler.options.Store.Network(ctx)
	if err != nil {
		scheduler.options.Logf("daemon: load network: %v", err)
		return
	}
	devices := append([]registry.DeviceRecord(nil), state.Devices...)
	sort.Slice(devices, func(left, right int) bool { return devices[left].ID < devices[right].ID })
	for _, workspace := range workspaces {
		scheduler.syncWorkspace(ctx, workspace, devices, endpoints)
	}
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
			result, err := peernetwork.Sync(ctx, workspace.Name, endpoints[peer.ID], peer, scheduler.options.Store, scheduler.options.Identity, scheduler.options.Name)
			if err != nil {
				scheduler.options.Logf("daemon: sync %s with %s: %v", workspace.Name, peer.Name, err)
				continue
			}
			scheduler.options.Logf("daemon: sync %s with %s: %s", workspace.Name, peer.Name, result.Status)
			continue
		}
		if err := peernetwork.WakeWorkspace(ctx, workspace.WorkspaceID, endpoints[peer.ID], peer, scheduler.options.Store, scheduler.options.Identity, scheduler.options.Name); err != nil {
			scheduler.options.Logf("daemon: wake %s for %s: %v", peer.Name, workspace.Name, err)
		}
	}
}
