package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
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

const (
	pairExchangeTimeout = 30 * time.Second
	maxPairMessageBytes = 1 << 20
	maxPairConnections  = 8
	maxPairCodeFailures = 5
)

var errInvalidPairCode = errors.New("pairing code is invalid")

type pairAttempt struct {
	result PairResult
	err    error
	retry  bool
	source string
}

type joinRequest struct {
	Version int    `json:"version"`
	Code    string `json:"code"`
	Name    string `json:"name"`
}

type pairChallenge struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
}

type joinConfirmation struct {
	Confirmed bool `json:"confirmed"`
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
	pairContext, cancel := context.WithCancel(ctx)
	defer cancel()
	return acceptPairAttempts(pairContext, listener, cert, code, options)
}

func acceptPairAttempts(ctx context.Context, listener net.Listener, cert tls.Certificate, code string, options PairOptions) (PairResult, error) {
	connections := make(chan net.Conn)
	acceptErrors := make(chan error, 1)
	go acceptPairConnections(ctx, listener, connections, acceptErrors)
	results := make(chan pairAttempt, maxPairConnections)
	semaphore := make(chan struct{}, maxPairConnections)
	failures := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return PairResult{}, ctx.Err()
		case err := <-acceptErrors:
			if ctx.Err() != nil {
				return PairResult{}, ctx.Err()
			}
			return PairResult{}, err
		case connection := <-connections:
			startPairAttempt(ctx, connection, cert, code, options, semaphore, results, failures)
		case attempt := <-results:
			if retryPairAttempt(attempt, failures) {
				continue
			}
			return attempt.result, attempt.err
		}
	}
}

func acceptPairConnections(ctx context.Context, listener net.Listener, connections chan<- net.Conn, acceptErrors chan<- error) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			select {
			case acceptErrors <- err:
			default:
			}
			return
		}
		select {
		case connections <- connection:
		case <-ctx.Done():
			_ = connection.Close()
			return
		}
	}
}

func startPairAttempt(ctx context.Context, connection net.Conn, cert tls.Certificate, code string, options PairOptions, semaphore chan struct{}, results chan<- pairAttempt, failures map[string]int) {
	source := pairSource(connection.RemoteAddr())
	if failures[source] >= maxPairCodeFailures {
		_ = connection.Close()
		return
	}
	select {
	case semaphore <- struct{}{}:
		go runPairAttempt(ctx, connection, cert, code, options, semaphore, results, source)
	default:
		_ = connection.Close()
	}
}

func retryPairAttempt(attempt pairAttempt, failures map[string]int) bool {
	if errors.Is(attempt.err, errInvalidPairCode) {
		failures[attempt.source]++
	}
	return attempt.retry
}

func runPairAttempt(ctx context.Context, connection net.Conn, cert tls.Certificate, code string, options PairOptions, semaphore chan struct{}, results chan<- pairAttempt, source string) {
	defer func() { <-semaphore }()
	defer func() { _ = connection.Close() }()
	attemptCtx, cancel := context.WithTimeout(ctx, pairExchangeTimeout)
	defer cancel()
	stop := watchConnection(attemptCtx, connection)
	defer stop()
	if err := writePairCertificate(connection, cert); err != nil {
		results <- pairAttempt{err: err, retry: true, source: source}
		return
	}
	tlsConnection := tls.Server(connection, pairingServerTLS(cert))
	result, retry, err := acceptPairConnection(attemptCtx, tlsConnection, code, options)
	results <- pairAttempt{result: result, err: err, retry: retry, source: source}
}

func acceptPairConnection(ctx context.Context, connection net.Conn, code string, options PairOptions) (PairResult, bool, error) {
	peerKey, peerName, retry, err := receivePairRequest(ctx, connection, code)
	if err != nil {
		return PairResult{}, retry, err
	}
	if err = confirmPairConnection(connection, peerName, authenticationString(options.Identity.PublicKey(), peerKey), options.Confirm); err != nil {
		return PairResult{}, false, err
	}
	return persistPairedDevice(ctx, connection, peerKey, peerName, options)
}

func receivePairRequest(ctx context.Context, connection net.Conn, code string) (ed25519.PublicKey, string, bool, error) {
	tlsConnection := connection.(*tls.Conn)
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		return nil, "", true, err
	}
	peerKey, peerCertificateName, err := peerPublicKey(tlsConnection.ConnectionState())
	if err != nil {
		return nil, "", true, err
	}
	var request joinRequest
	if err = decodeLimited(connection, maxPairMessageBytes, &request); err != nil {
		return nil, "", true, err
	}
	if request.Version != 1 || request.Code != code {
		_ = json.NewEncoder(connection).Encode(pairChallenge{Error: "pairing code is invalid"})
		return nil, "", true, errInvalidPairCode
	}
	if err = json.NewEncoder(connection).Encode(pairChallenge{Accepted: true}); err != nil {
		return nil, "", false, err
	}
	peerName := request.Name
	if peerName == "" {
		peerName = peerCertificateName
	}
	return peerKey, peerName, false, nil
}

func pairSource(address net.Addr) string {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return address.String()
	}
	return host
}

func confirmPairConnection(connection net.Conn, peerName, authentication string, confirm Confirm) error {
	confirmed, err := confirm(peerName, authentication)
	if err != nil {
		return err
	}
	if !confirmed {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: "pairing rejected"})
		return errors.New("pairing rejected")
	}
	var peerConfirmation joinConfirmation
	if err = decodeLimited(connection, maxPairMessageBytes, &peerConfirmation); err != nil {
		return err
	}
	if !peerConfirmation.Confirmed {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: "joining device did not confirm pairing"})
		return errors.New("joining device did not confirm pairing")
	}
	return nil
}

func persistPairedDevice(ctx context.Context, connection net.Conn, peerKey ed25519.PublicKey, peerName string, options PairOptions) (PairResult, bool, error) {
	if _, err := options.Store.EnsureNetwork(ctx, options.Name); err != nil {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: err.Error()})
		return PairResult{}, false, err
	}
	state, err := options.Store.AddNetworkDevice(ctx, peerName, peerKey, options.Role)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(pairResponse{Error: err.Error()})
		return PairResult{}, false, err
	}
	bundle, err := options.Store.ExportNetwork(ctx)
	if err != nil {
		return PairResult{}, false, err
	}
	if err = json.NewEncoder(connection).Encode(pairResponse{Accepted: true, Network: bundle}); err != nil {
		return PairResult{}, false, err
	}
	peer, found := deviceRecord(state.Devices, peerKey)
	if !found {
		return PairResult{}, false, errors.New("paired device is missing from network state")
	}
	return PairResult{Peer: peer}, false, nil
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
	ctx, cancel := context.WithTimeout(ctx, pairExchangeTimeout)
	defer cancel()
	if err := requireUnpairedStore(ctx, options.Store); err != nil {
		return PairResult{}, err
	}
	return joinEndpoint(ctx, endpoint, options)
}

func joinEndpoint(ctx context.Context, endpoint string, options JoinOptions) (PairResult, error) {
	cert, err := certificate(options.Identity, options.Name)
	if err != nil {
		return PairResult{}, err
	}
	dialer := net.Dialer{}
	rawConnection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return PairResult{}, err
	}
	stop := watchConnection(ctx, rawConnection)
	defer stop()
	peerCertificate, err := readPairCertificate(rawConnection)
	if err != nil {
		_ = rawConnection.Close()
		return PairResult{}, err
	}
	connection := tls.Client(rawConnection, pairingClientTLS(cert, peerCertificate))
	if err = connection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return PairResult{}, err
	}
	defer func() { _ = connection.Close() }()
	peerKey, peerName, err := peerPublicKey(connection.ConnectionState())
	if err != nil {
		return PairResult{}, err
	}
	authentication := authenticationString(options.Identity.PublicKey(), peerKey)
	if err = json.NewEncoder(connection).Encode(joinRequest{Version: 1, Code: options.Code, Name: options.Name}); err != nil {
		return PairResult{}, err
	}
	var challenge pairChallenge
	if err = decodeLimited(connection, maxPairMessageBytes, &challenge); err != nil {
		return PairResult{}, err
	}
	if !challenge.Accepted {
		return PairResult{}, fmt.Errorf("pairing failed: %s", challenge.Error)
	}
	confirmed, err := options.Confirm(peerName, authentication)
	if err != nil {
		return PairResult{}, err
	}
	if err = json.NewEncoder(connection).Encode(joinConfirmation{Confirmed: confirmed}); err != nil {
		return PairResult{}, err
	}
	var response pairResponse
	if err = decodeLimited(connection, maxPairMessageBytes, &response); err != nil {
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

func requireUnpairedStore(ctx context.Context, store *registry.Store) error {
	if store == nil {
		return errors.New("join store is required")
	}
	if _, err := store.Network(ctx); err == nil {
		return errors.New("device already belongs to a network")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func writePairCertificate(connection net.Conn, cert tls.Certificate) error {
	body := cert.Certificate[0]
	if len(body) > 0xffff {
		return errors.New("pairing certificate is too large")
	}
	if _, err := fmt.Fprintf(connection, "%04x", len(body)); err != nil {
		return err
	}
	_, err := connection.Write(body)
	return err
}

func readPairCertificate(connection net.Conn) (*x509.Certificate, error) {
	var size [4]byte
	if _, err := io.ReadFull(connection, size[:]); err != nil {
		return nil, err
	}
	length, err := strconv.ParseUint(string(size[:]), 16, 16)
	if err != nil {
		return nil, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(connection, body); err != nil {
		return nil, err
	}
	return x509.ParseCertificate(body)
}

func pairingCode() (string, error) {
	var body [12]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%06d", hex.EncodeToString(body[:8]), binary.BigEndian.Uint32(body[8:])%1000000), nil
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
