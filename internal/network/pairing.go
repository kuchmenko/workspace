package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	"github.com/kuchmenko/workspace/internal/registry"
)

type Confirm func(peerName, authenticationString string) (bool, error)

type PairOptions struct {
	Store            *registry.Store
	Identity         device.Identity
	Name             string
	Role             string
	Confirm          Confirm
	ListenAddress    string
	DisableDiscovery bool
	Ready            func(code, endpoint string)
}

type JoinOptions struct {
	Store    *registry.Store
	Identity device.Identity
	Name     string
	Code     string
	Confirm  Confirm
}

type PairResult struct {
	Peer registry.DeviceRecord
}

type joinRequest struct {
	Version   int    `json:"version"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Confirmed bool   `json:"confirmed"`
}

type pairResponse struct {
	Accepted bool                   `json:"accepted"`
	Error    string                 `json:"error,omitempty"`
	Network  registry.NetworkBundle `json:"network"`
}

func Pair(ctx context.Context, options PairOptions) (PairResult, error) {
	if options.Store == nil || options.Confirm == nil {
		return PairResult{}, errors.New("pair store and confirmation are required")
	}
	if options.Role == "" {
		options.Role = registry.NetworkMember
	}
	if _, err := options.Store.EnsureNetwork(ctx, options.Name); err != nil {
		return PairResult{}, err
	}
	cert, err := certificate(options.Identity, options.Name)
	if err != nil {
		return PairResult{}, err
	}
	listenAddress := options.ListenAddress
	if listenAddress == "" {
		listenAddress = ":0"
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return PairResult{}, err
	}
	defer func() { _ = listener.Close() }()
	code, err := pairingCode()
	if err != nil {
		return PairResult{}, err
	}
	var advertisement *advertisement
	if !options.DisableDiscovery {
		port := listener.Addr().(*net.TCPAddr).Port
		advertisement, err = advertisePair(discoveryInstance(options.Name, options.Identity.ID()), code, port)
		if err != nil {
			return PairResult{}, err
		}
		defer advertisement.Close()
	}
	if options.Ready != nil {
		options.Ready(code, listener.Addr().String())
	}
	connection, err := acceptContext(ctx, tls.NewListener(listener, pairingServerTLS(cert)))
	if err != nil {
		return PairResult{}, err
	}
	defer func() { _ = connection.Close() }()
	tlsConnection := connection.(*tls.Conn)
	if err = tlsConnection.HandshakeContext(ctx); err != nil {
		return PairResult{}, err
	}
	peerKey, peerCertificateName, err := peerPublicKey(tlsConnection.ConnectionState())
	if err != nil {
		return PairResult{}, err
	}
	var request joinRequest
	if err = json.NewDecoder(connection).Decode(&request); err != nil {
		return PairResult{}, err
	}
	if request.Version != 1 || request.Code != code || !request.Confirmed {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: "joining device did not confirm pairing"})
		return PairResult{}, errors.New("joining device did not confirm pairing")
	}
	peerName := request.Name
	if peerName == "" {
		peerName = peerCertificateName
	}
	authentication := authenticationString(options.Identity.PublicKey(), peerKey)
	confirmed, err := options.Confirm(peerName, authentication)
	if err != nil {
		return PairResult{}, err
	}
	if !confirmed {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: "pairing rejected"})
		return PairResult{}, errors.New("pairing rejected")
	}
	state, err := options.Store.AddNetworkDevice(ctx, peerName, peerKey, options.Role)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: err.Error()})
		return PairResult{}, err
	}
	bundle, err := options.Store.ExportNetwork(ctx)
	if err != nil {
		return PairResult{}, err
	}
	if err = json.NewEncoder(connection).Encode(pairResponse{Accepted: true, Network: bundle}); err != nil {
		return PairResult{}, err
	}
	peer, found := deviceRecord(state.Devices, peerKey)
	if !found {
		return PairResult{}, errors.New("paired device is missing from network state")
	}
	return PairResult{Peer: peer}, nil
}

func Join(ctx context.Context, options JoinOptions) (PairResult, error) {
	if options.Store == nil || options.Confirm == nil {
		return PairResult{}, errors.New("join store and confirmation are required")
	}
	discoveryContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint, err := discoverPair(discoveryContext, options.Code)
	if err != nil {
		return PairResult{}, err
	}
	return JoinEndpoint(ctx, endpoint, options)
}

func JoinEndpoint(ctx context.Context, endpoint string, options JoinOptions) (PairResult, error) {
	cert, err := certificate(options.Identity, options.Name)
	if err != nil {
		return PairResult{}, err
	}
	dialer := tls.Dialer{Config: pairingClientTLS(cert)}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return PairResult{}, err
	}
	defer func() { _ = connection.Close() }()
	tlsConnection := connection.(*tls.Conn)
	peerKey, peerName, err := peerPublicKey(tlsConnection.ConnectionState())
	if err != nil {
		return PairResult{}, err
	}
	authentication := authenticationString(options.Identity.PublicKey(), peerKey)
	confirmed, err := options.Confirm(peerName, authentication)
	if err != nil {
		return PairResult{}, err
	}
	if err = json.NewEncoder(connection).Encode(joinRequest{Version: 1, Code: options.Code, Name: options.Name, Confirmed: confirmed}); err != nil {
		return PairResult{}, err
	}
	var response pairResponse
	if err = json.NewDecoder(connection).Decode(&response); err != nil {
		return PairResult{}, err
	}
	if !response.Accepted {
		return PairResult{}, fmt.Errorf("pairing failed: %s", response.Error)
	}
	state, err := options.Store.ImportNetwork(ctx, response.Network, device.IDForPublicKey(peerKey))
	if err != nil {
		return PairResult{}, err
	}
	peer, found := deviceRecord(state.Devices, peerKey)
	if !found {
		return PairResult{}, errors.New("inviting device is missing from network state")
	}
	return PairResult{Peer: peer}, nil
}

func pairingCode() (string, error) {
	var body [4]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(body[:])%1000000), nil
}

func acceptContext(ctx context.Context, listener net.Listener) (net.Conn, error) {
	result := make(chan struct {
		connection net.Conn
		err        error
	}, 1)
	go func() {
		connection, err := listener.Accept()
		result <- struct {
			connection net.Conn
			err        error
		}{connection: connection, err: err}
	}()
	select {
	case accepted := <-result:
		return accepted.connection, accepted.err
	case <-ctx.Done():
		_ = listener.Close()
		return nil, ctx.Err()
	}
}

func deviceRecord(devices []registry.DeviceRecord, publicKey ed25519.PublicKey) (registry.DeviceRecord, bool) {
	id := device.IDForPublicKey(publicKey)
	for _, record := range devices {
		if record.ID == id {
			return record, true
		}
	}
	return registry.DeviceRecord{}, false
}
