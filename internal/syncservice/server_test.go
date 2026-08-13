package syncservice

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type testService struct {
	store    *Store
	identity *Identity
	server   *Server
	url      string
}

func startTestService(t *testing.T) *testService {
	t.Helper()
	directory := t.TempDir()
	store, err := Open(filepath.Join(directory, "store", "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := OpenIdentity(filepath.Join(directory, "identity"), nil)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, identity, directory, "")
	go func() { _ = server.Serve(listener) }()
	service := &testService{store: store, identity: identity, server: server, url: "https://" + listener.Addr().String()}
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = store.Close()
	})
	return service
}

func (s *testService) httpClient(certificate tls.Certificate) *http.Client {
	config := s.identity.ClientTLSConfig("127.0.0.1", certificate)
	return &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
}

func csr(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored-admin-claim"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), key
}

func pairClient(t *testing.T, service *testService, role Role) (PairResponse, tls.Certificate) {
	t.Helper()
	pairing, err := service.store.CreatePairing(role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, key := csr(t)
	request := PairRequest{AttemptID: "attempt-" + string(role), Token: pairing.Token, DisplayName: string(role), CSR: csrPEM}
	response := postJSON[PairResponse](t, service.httpClient(tls.Certificate{}), service.url+"/v1/pair", request, http.StatusOK)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair([]byte(response.Certificate), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return response, certificate
}

func postJSON[T any](t *testing.T, client *http.Client, url string, request any, status int) T {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s status = %d, want %d: %s", url, response.StatusCode, status, data)
	}
	var result T
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLoopbackDiscoveryIsPublicAndMinimal(t *testing.T) {
	service := startTestService(t)
	response, err := service.httpClient(tls.Certificate{}).Get(service.url + "/v1/discovery")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(service.identity.Fingerprint())) {
		t.Fatalf("discovery = %d %s", response.StatusCode, body)
	}
	for _, forbidden := range []string{"workspace_id", "revision", "actor_id", "client"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("discovery leaked %q: %s", forbidden, body)
		}
	}
}

func TestPairingReplayAndTokenBinding(t *testing.T) {
	service := startTestService(t)
	pairing, err := service.store.CreatePairing(RoleAdmin, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, _ := csr(t)
	request := PairRequest{AttemptID: "stable-attempt", Token: pairing.Token, DisplayName: "machine", CSR: csrPEM}
	first, err := service.store.Pair(context.Background(), request, service.identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.store.Pair(context.Background(), request, service.identity)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("retry = %v %+v, want %+v", err, second, first)
	}
	request.CSR, _ = csr(t)
	if _, err = service.store.Pair(context.Background(), request, service.identity); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("different CSR error = %v", err)
	}
	request.CSR = csrPEM
	request.AttemptID = "different"
	if _, err = service.store.Pair(context.Background(), request, service.identity); !errors.Is(err, ErrPairingMismatch) {
		t.Fatalf("different attempt error = %v", err)
	}
}

func TestExpiredPairingFails(t *testing.T) {
	service := startTestService(t)
	pairing, err := service.store.CreatePairing(RoleClient, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	csrPEM, _ := csr(t)
	_, err = service.store.Pair(context.Background(), PairRequest{AttemptID: "expired", Token: pairing.Token, DisplayName: "late", CSR: csrPEM}, service.identity)
	if !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestPairedStatusUnknownAndRevokedClients(t *testing.T) {
	service := startTestService(t)
	paired, certificate := pairClient(t, service, RoleClient)
	response, err := service.httpClient(certificate).Get(service.url + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status struct {
		ActorID string `json:"actor_id"`
		Role    Role   `json:"role"`
	}
	if err = json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != http.StatusOK || status.ActorID != paired.ActorID || status.Role != RoleClient {
		t.Fatalf("status = %d %v %+v", response.StatusCode, err, status)
	}
	unknownCSR, unknownKey := csr(t)
	issued, _, _, err := service.identity.issueClient(unknownCSR, "not-registered")
	if err != nil {
		t.Fatal(err)
	}
	unknownKeyDER, _ := x509.MarshalPKCS8PrivateKey(unknownKey)
	unknownCert, err := tls.X509KeyPair(issued, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: unknownKeyDER}))
	if err != nil {
		t.Fatal(err)
	}
	unknownResponse, err := service.httpClient(unknownCert).Get(service.url + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	unknownResponse.Body.Close()
	if unknownResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown status = %d", unknownResponse.StatusCode)
	}
	if err = service.store.RevokeClient(context.Background(), paired.ActorID); err != nil {
		t.Fatal(err)
	}
	revokedResponse, err := service.httpClient(certificate).Get(service.url + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	revokedResponse.Body.Close()
	if revokedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d", revokedResponse.StatusCode)
	}
}

func TestAdminAuthorizationImportAndPairingCreation(t *testing.T) {
	service := startTestService(t)
	_, clientCertificate := pairClient(t, service, RoleClient)
	_, adminCertificate := pairClient(t, service, RoleAdmin)
	request := struct {
		Role       Role  `json:"role"`
		TTLSeconds int64 `json:"ttl_seconds"`
	}{Role: RoleClient, TTLSeconds: 60}
	postJSON[map[string]any](t, service.httpClient(clientCertificate), service.url+"/v1/admin/pairings", request, http.StatusForbidden)
	pairing := postJSON[Pairing](t, service.httpClient(adminCertificate), service.url+"/v1/admin/pairings", request, http.StatusCreated)
	if pairing.Token == "" {
		t.Fatal("admin pairing token is empty")
	}
	importRequest := struct {
		Workspace []byte `json:"workspace"`
	}{canonical(t, workspace("base"))}
	postJSON[map[string]any](t, service.httpClient(clientCertificate), service.url+"/v1/workspaces/import", importRequest, http.StatusForbidden)
	ref := postJSON[StateRef](t, service.httpClient(adminCertificate), service.url+"/v1/workspaces/import", importRequest, http.StatusCreated)
	if ref.WorkspaceID == "" {
		t.Fatal("workspace was not imported")
	}
	retry := postJSON[StateRef](t, service.httpClient(adminCertificate), service.url+"/v1/workspaces/import", importRequest, http.StatusCreated)
	if retry != ref {
		t.Fatalf("import retry = %+v, want %+v", retry, ref)
	}
	different := struct {
		Workspace []byte `json:"workspace"`
	}{canonical(t, workspace("other"))}
	postJSON[map[string]any](t, service.httpClient(adminCertificate), service.url+"/v1/workspaces/import", different, http.StatusConflict)
}

func TestAdminUpgradeBacksUpBeforeUpdaterAndForbidsClient(t *testing.T) {
	service := startTestService(t)
	_, clientCertificate := pairClient(t, service, RoleClient)
	_, adminCertificate := pairClient(t, service, RoleAdmin)
	socketPath := filepath.Join(t.TempDir(), "updater.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	service.server.updaterSocket = socketPath
	service.server.stateDir = filepath.Dir(filepath.Dir(service.storeDBPath(t)))
	received := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			received <- acceptErr
			return
		}
		defer connection.Close()
		backups, globErr := filepath.Glob(filepath.Join(service.server.stateDir, "backups", "service-*.db"))
		if globErr != nil || len(backups) != 1 {
			received <- fmt.Errorf("backup before request = %v, %v", backups, globErr)
			return
		}
		var request UpdaterRequest
		if decodeErr := json.NewDecoder(connection).Decode(&request); decodeErr != nil {
			received <- decodeErr
			return
		}
		if request.Version != "v1.2.3" {
			received <- fmt.Errorf("version = %q", request.Version)
			return
		}
		if request.BackupPath != backups[0] {
			received <- fmt.Errorf("backup path = %q, want %q", request.BackupPath, backups[0])
			return
		}
		received <- json.NewEncoder(connection).Encode(UpgradeResponse{Accepted: true, Version: request.Version})
	}()
	postJSON[map[string]any](t, service.httpClient(clientCertificate), service.url+"/v1/admin/upgrade", UpgradeRequest{Version: "v1.2.3"}, http.StatusForbidden)
	result := postJSON[UpgradeResponse](t, service.httpClient(adminCertificate), service.url+"/v1/admin/upgrade", UpgradeRequest{Version: "v1.2.3"}, http.StatusAccepted)
	if err = <-received; err != nil || !result.Accepted || result.Backup == "" {
		t.Fatalf("upgrade = %+v, %v", result, err)
	}
	postJSON[map[string]any](t, service.httpClient(adminCertificate), service.url+"/v1/admin/upgrade", map[string]string{"version": "1.2.3"}, http.StatusBadRequest)
	postJSON[map[string]any](t, service.httpClient(adminCertificate), service.url+"/v1/admin/upgrade", map[string]string{"version": "v1.2.3", "url": "bad"}, http.StatusBadRequest)
}

func (s *testService) storeDBPath(t *testing.T) string {
	t.Helper()
	var path string
	if err := s.store.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncUsesCertificateActor(t *testing.T) {
	service := startTestService(t)
	paired, certificate := pairClient(t, service, RoleAdmin)
	ref, err := service.store.AdminImportSingleWorkspace(context.Background(), canonical(t, workspace("base")))
	if err != nil {
		t.Fatal(err)
	}
	request := requestFor(t, ref, "certificate-actor", "changed")
	postJSON[SyncResponse](t, service.httpClient(certificate), service.url+"/v1/sync", request, http.StatusOK)
	var actorID string
	if err = service.store.db.QueryRow(`SELECT actor_id FROM requests WHERE request_id=?`, request.RequestID).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if actorID != paired.ActorID {
		t.Fatalf("stored actor = %q, want %q", actorID, paired.ActorID)
	}
	body, _ := json.Marshal(request)
	body = bytes.TrimSuffix(body, []byte("}"))
	body = append(body, []byte(`,"actor_id":"forged"}`)...)
	response, err := service.httpClient(certificate).Post(service.url+"/v1/sync", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("body actor status = %d", response.StatusCode)
	}
}

func TestIdentityIsStableAndPrivate(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	first, err := OpenIdentity(directory, []string{"sync.lan", "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenIdentity(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("CA fingerprint changed")
	}
	firstLeaf, err := x509.ParseCertificate(first.serverCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	secondLeaf, err := x509.ParseCertificate(second.serverCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstLeaf.Raw, secondLeaf.Raw) {
		t.Fatal("server certificate changed on reopen")
	}
	if len(second.serverCert.Certificate) != 2 {
		t.Fatalf("reopened certificate chain has %d certificates", len(second.serverCert.Certificate))
	}
	if !slices.Contains(firstLeaf.DNSNames, "sync.lan") || !firstLeaf.IPAddresses[2].Equal(net.ParseIP("192.0.2.1")) {
		t.Fatalf("certificate names = %v %v", firstLeaf.DNSNames, firstLeaf.IPAddresses)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(directory)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o", info.Mode().Perm())
	}
	for _, entry := range entries {
		info, err = entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", entry.Name(), info.Mode().Perm())
		}
	}
}
