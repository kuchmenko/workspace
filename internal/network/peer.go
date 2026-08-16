package network

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

const DefaultListenAddress = ":17337"

const (
	peerExchangeTimeout = 10 * time.Second
	maxPeerMessageBytes = 64 << 20
	maxPeerConnections  = 32
)

type RejectedError struct {
	err error
}

type UnavailableError struct {
	err error
}

func (err RejectedError) Error() string {
	return err.err.Error()
}

func (err RejectedError) Unwrap() error {
	return err.err
}

func IsRejected(err error) bool {
	var rejected RejectedError
	return errors.As(err, &rejected)
}

func (err UnavailableError) Error() string {
	return err.err.Error()
}

func (err UnavailableError) Unwrap() error {
	return err.err
}

func IsUnavailable(err error) bool {
	var unavailable UnavailableError
	return errors.As(err, &unavailable)
}

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
	Version     int                    `json:"version"`
	Action      string                 `json:"action"`
	Network     registry.NetworkBundle `json:"network"`
	WorkspaceID string                 `json:"workspace_id,omitempty"`
	Workspace   *registry.Bundle       `json:"workspace,omitempty"`
}

type peerResponse struct {
	Info       PeerInfo                    `json:"info"`
	Network    registry.NetworkBundle      `json:"network"`
	Workspaces []registry.WorkspaceSummary `json:"workspaces,omitempty"`
	Workspace  *registry.Bundle            `json:"workspace,omitempty"`
	Conflicts  []registry.Conflict         `json:"conflicts,omitempty"`
	SyncStatus string                      `json:"sync_status,omitempty"`
	Error      string                      `json:"error,omitempty"`
}

func Serve(ctx context.Context, options ServeOptions) error {
	self, cert, listener, err := preparePeerServer(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	tlsListener := tls.NewListener(listener, peerServerTLS(cert, options.Identity, options.Name, trustedPeer(options.Store)))
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
	slots := make(chan struct{}, maxPeerConnections)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		connection, acceptErr := tlsListener.Accept()
		if acceptErr != nil {
			<-slots
			if ctx.Err() != nil {
				return nil
			}
			return acceptErr
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-slots }()
			defer func() { _ = connection.Close() }()
			_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
			servePeerConnection(options.Store, self, connection)
		}()
	}
}

func Probe(ctx context.Context, endpoint string, target registry.DeviceRecord, store *registry.Store, identity device.Identity, name string) (PeerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, peerExchangeTimeout)
	defer cancel()
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
	stop := watchConnection(ctx, connection)
	defer stop()
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return PeerInfo{}, err
	}
	if err = json.NewEncoder(connection).Encode(peerRequest{Version: 1, Action: "status", Network: bundle}); err != nil {
		return PeerInfo{}, err
	}
	var response peerResponse
	if err = decodeLimited(connection, maxPeerMessageBytes, &response); err != nil {
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
	if err := decodeLimited(connection, maxPeerMessageBytes, &request); err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	if request.Version != 1 {
		_ = encoder.Encode(peerResponse{Error: "unsupported peer request"})
		return
	}
	if _, err := store.MergeNetwork(context.Background(), request.Network); err != nil {
		_ = encoder.Encode(peerResponse{Error: err.Error()})
		return
	}
	peerID, err := authenticatedPeerID(connection)
	if err != nil {
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
	response := peerResponse{Info: PeerInfo{DeviceID: self.ID, Name: self.Name, NetworkID: state.ID, Epoch: state.Epoch}, Network: bundle}
	if err = handleWorkspaceRequest(context.Background(), store, peerID, request, &response); err != nil {
		response.Error = err.Error()
	}
	_ = encoder.Encode(response)
}

func authenticatedPeerID(connection net.Conn) (string, error) {
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return "", errors.New("peer connection is not TLS")
	}
	if err := tlsConnection.Handshake(); err != nil {
		return "", err
	}
	public, _, err := peerPublicKey(tlsConnection.ConnectionState())
	if err != nil {
		return "", err
	}
	return device.IDForPublicKey(public), nil
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

func watchConnection(ctx context.Context, connection net.Conn) func() {
	if deadline, present := ctx.Deadline(); present {
		_ = connection.SetDeadline(deadline)
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopped:
		}
	}()
	return func() { close(stopped) }
}

func decodeLimited(reader io.Reader, limit int64, value any) error {
	return json.NewDecoder(io.LimitReader(reader, limit)).Decode(value)
}
