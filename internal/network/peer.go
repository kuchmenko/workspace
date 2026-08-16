package network

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

const DefaultListenAddress = ":17337"

type ServeOptions struct {
	Store            *registry.Store
	Identity         device.Identity
	Name             string
	ListenAddress    string
	DisableDiscovery bool
	Ready            func(endpoint string)
}

type PeerInfo struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	NetworkID string `json:"network_id"`
	Epoch     int64  `json:"epoch"`
}

type Status struct {
	Device   registry.DeviceRecord `json:"device"`
	Online   bool                  `json:"online"`
	Endpoint string                `json:"endpoint,omitempty"`
	Error    string                `json:"error,omitempty"`
}

type peerRequest struct {
	Version int                    `json:"version"`
	Action  string                 `json:"action"`
	Network registry.NetworkBundle `json:"network"`
}

type peerResponse struct {
	Info    PeerInfo               `json:"info"`
	Network registry.NetworkBundle `json:"network"`
	Error   string                 `json:"error,omitempty"`
}

func Serve(ctx context.Context, options ServeOptions) error {
	self, cert, listener, err := preparePeerServer(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	tlsListener := tls.NewListener(listener, peerServerTLS(cert, trustedPeer(options.Store)))
	return servePeerListener(ctx, options, self, listener, tlsListener)
}

func preparePeerServer(ctx context.Context, options ServeOptions) (registry.DeviceRecord, tls.Certificate, net.Listener, error) {
	if options.Store == nil {
		return registry.DeviceRecord{}, tls.Certificate{}, nil, errors.New("peer store is required")
	}
	state, err := options.Store.Network(ctx)
	if err != nil {
		return registry.DeviceRecord{}, tls.Certificate{}, nil, err
	}
	self, found := activeDevice(state.Devices, options.Identity.ID())
	if !found {
		return registry.DeviceRecord{}, tls.Certificate{}, nil, errors.New("local device is not active in the network")
	}
	cert, err := certificate(options.Identity, options.Name)
	if err != nil {
		return registry.DeviceRecord{}, tls.Certificate{}, nil, err
	}
	address := options.ListenAddress
	if address == "" {
		address = DefaultListenAddress
	}
	listener, err := net.Listen("tcp", address)
	return self, cert, listener, err
}

func trustedPeer(store *registry.Store) func(string) bool {
	return func(id string) bool {
		current, loadErr := store.Network(context.Background())
		if loadErr != nil {
			return false
		}
		_, trusted := activeDevice(current.Devices, id)
		return trusted
	}
}

func servePeerListener(ctx context.Context, options ServeOptions, self registry.DeviceRecord, listener net.Listener, tlsListener net.Listener) error {
	var broadcast *advertisement
	var err error
	if !options.DisableDiscovery {
		port := listener.Addr().(*net.TCPAddr).Port
		broadcast, err = advertisePeer(discoveryInstance(self.Name, self.ID), self.ID, port)
		if err != nil {
			return err
		}
		defer broadcast.Close()
	}
	if options.Ready != nil {
		options.Ready(listener.Addr().String())
	}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-stopped:
		}
	}()
	var wait sync.WaitGroup
	defer wait.Wait()
	for {
		connection, acceptErr := tlsListener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return acceptErr
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { _ = connection.Close() }()
			_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
			servePeerConnection(options.Store, self, connection)
		}()
	}
}

func Probe(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string) (PeerInfo, error) {
	cert, err := certificate(identity, name)
	if err != nil {
		return PeerInfo{}, err
	}
	config, err := peerClientTLS(cert, ed25519.PublicKey(target.PublicKey), target.Name)
	if err != nil {
		return PeerInfo{}, err
	}
	dialer := tls.Dialer{Config: config}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return PeerInfo{}, err
	}
	defer func() { _ = connection.Close() }()
	if deadline, present := ctx.Deadline(); present {
		_ = connection.SetDeadline(deadline)
	}
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return PeerInfo{}, err
	}
	if err = json.NewEncoder(connection).Encode(peerRequest{Version: 1, Action: "status", Network: bundle}); err != nil {
		return PeerInfo{}, err
	}
	var response peerResponse
	if err = json.NewDecoder(connection).Decode(&response); err != nil {
		return PeerInfo{}, err
	}
	if response.Error != "" {
		return PeerInfo{}, errors.New(response.Error)
	}
	if response.Info.DeviceID != target.ID {
		return PeerInfo{}, errors.New("peer response identity does not match certificate")
	}
	if _, err = store.MergeNetwork(ctx, response.Network); err != nil {
		return PeerInfo{}, err
	}
	return response.Info, nil
}

func NetworkStatus(ctx context.Context, store *registry.Store, identity device.Identity, name string, discoveryWindow time.Duration) ([]Status, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return nil, err
	}
	discoveryContext, cancel := context.WithTimeout(ctx, discoveryWindow)
	endpoints, err := discoverPeers(discoveryContext)
	cancel()
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(state.Devices))
	for _, record := range state.Devices {
		status := Status{Device: record}
		if record.ID == identity.ID() {
			status.Online = true
			status.Endpoint = "local"
		} else if endpoint := endpoints[record.ID]; endpoint != "" && record.Active {
			probeContext, stop := context.WithTimeout(ctx, 3*time.Second)
			_, probeErr := Probe(probeContext, endpoint, record, store, identity, name)
			stop()
			status.Endpoint = endpoint
			status.Online = probeErr == nil
			if probeErr != nil {
				status.Error = probeErr.Error()
			}
		}
		statuses = append(statuses, status)
	}
	current, err := store.Network(ctx)
	if err != nil {
		return nil, err
	}
	for index := range statuses {
		if record, found := deviceByID(current.Devices, statuses[index].Device.ID); found {
			statuses[index].Device = record
		}
	}
	sort.Slice(statuses, func(left, right int) bool { return statuses[left].Device.Name < statuses[right].Device.Name })
	return statuses, nil
}

func servePeerConnection(store *registry.Store, self registry.DeviceRecord, connection net.Conn) {
	var request peerRequest
	encoder := json.NewEncoder(connection)
	if err := json.NewDecoder(connection).Decode(&request); err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	if request.Version != 1 || request.Action != "status" {
		_ = encoder.Encode(peerResponse{Error: "unsupported peer request"})
		return
	}
	if _, err := store.MergeNetwork(context.Background(), request.Network); err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	state, err := store.Network(context.Background())
	if err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	bundle, err := store.ExportNetwork(context.Background())
	if err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	_ = encoder.Encode(peerResponse{Info: PeerInfo{DeviceID: self.ID, Name: self.Name, NetworkID: state.ID, Epoch: state.Epoch}, Network: bundle})
}

func activeDevice(devices []registry.DeviceRecord, id string) (registry.DeviceRecord, bool) {
	record, found := deviceByID(devices, id)
	return record, found && record.Active
}

func deviceByID(devices []registry.DeviceRecord, id string) (registry.DeviceRecord, bool) {
	for _, record := range devices {
		if record.ID == id {
			return record, true
		}
	}
	return registry.DeviceRecord{}, false
}
