package syncservice

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/kuchmenko/workspace/internal/buildinfo"
)

const maxRequestBody = 4 << 20

type Server struct {
	store         *Store
	identity      *Identity
	http          *http.Server
	updaterSocket string
	stateDir      string
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, Client)

func NewServer(store *Store, identity *Identity, stateDir, updaterSocket string) *Server {
	server := &Server{store: store, identity: identity, stateDir: stateDir, updaterSocket: updaterSocket}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/discovery", server.discovery)
	mux.HandleFunc("POST /v1/pair", server.pair)
	mux.HandleFunc("GET /v1/status", server.protected(server.status))
	mux.HandleFunc("POST /v1/workspaces/import", server.protected(server.importWorkspace))
	mux.HandleFunc("GET /v1/workspaces/{id}", server.protected(server.currentWorkspace))
	mux.HandleFunc("POST /v1/sync", server.protected(server.sync))
	mux.HandleFunc("POST /v1/admin/pairings", server.protected(server.createPairing))
	mux.HandleFunc("POST /v1/admin/clients/{id}/revoke", server.protected(server.revokeClient))
	mux.HandleFunc("POST /v1/admin/upgrade", server.protected(server.upgrade))
	server.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return server
}

func (s *Server) Serve(listener net.Listener) error {
	return s.http.Serve(tlsListener(listener, s.identity))
}

func tlsListener(listener net.Listener, identity *Identity) net.Listener {
	return tls.NewListener(listener, identity.ServerTLSConfig())
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	ref := s.store.ServiceRef()
	writeJSON(w, http.StatusOK, struct {
		ServiceID     string `json:"service_id"`
		ServiceEpoch  string `json:"service_epoch"`
		Protocol      int    `json:"protocol_version"`
		CAFingerprint string `json:"ca_fingerprint"`
	}{ref.ServiceID, ref.ServiceEpoch, ProtocolVersion, s.identity.Fingerprint()})
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var request PairRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	response, err := s.store.Pair(r.Context(), request, s.identity)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPairingInvalid) || errors.Is(err, ErrPairingMismatch) {
			status = http.StatusUnauthorized
		}
		writeError(w, status, "pairing_rejected")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) protected(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			writeError(w, http.StatusUnauthorized, "client_certificate_required")
			return
		}
		certificate := r.TLS.PeerCertificates[0]
		publicHash, err := certificatePublicKeyHash(certificate)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "client_unauthorized")
			return
		}
		client, err := s.store.AuthenticateClient(r.Context(), certificate.SerialNumber.String(), publicHash)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "client_unauthorized")
			return
		}
		next(w, r, client)
	}
}

func certificatePublicKeyHash(certificate *x509.Certificate) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(der)
	return fmt.Sprintf("%x", hash), nil
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request, client Client) {
	ref := s.store.ServiceRef()
	writeJSON(w, http.StatusOK, struct {
		ServiceID    string `json:"service_id"`
		ServiceEpoch string `json:"service_epoch"`
		Protocol     int    `json:"protocol_version"`
		ActorID      string `json:"actor_id"`
		Role         Role   `json:"role"`
		Version      string `json:"version"`
	}{ref.ServiceID, ref.ServiceEpoch, ProtocolVersion, client.ActorID, client.Role, buildinfo.Version})
}

var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func ValidUpgradeVersion(version string) bool {
	return version == "latest" || releaseVersion.MatchString(version)
}

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, client Client) {
	if !requireAdmin(w, client) {
		return
	}
	var request UpgradeRequest
	if decodeJSON(w, r, &request) != nil {
		return
	}
	if !ValidUpgradeVersion(request.Version) || s.updaterSocket == "" || s.stateDir == "" {
		writeError(w, http.StatusBadRequest, "invalid_upgrade")
		return
	}
	backup, err := s.store.Backup(s.stateDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup_failed")
		return
	}
	connection, err := net.DialTimeout("unix", s.updaterSocket, 5*time.Second)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "updater_unavailable")
		return
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))
	internalRequest := UpdaterRequest{Version: request.Version, BackupPath: backup}
	if err = json.NewEncoder(connection).Encode(internalRequest); err != nil {
		writeError(w, http.StatusBadGateway, "updater_failed")
		return
	}
	var result UpgradeResponse
	decoder := json.NewDecoder(io.LimitReader(connection, 4097))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil || !result.Accepted {
		writeError(w, http.StatusBadGateway, "updater_failed")
		return
	}
	result.Backup = backup
	writeJSON(w, http.StatusAccepted, result)
	_ = os.Chmod(backup, 0o600)
}

func (s *Server) importWorkspace(w http.ResponseWriter, r *http.Request, client Client) {
	if !requireAdmin(w, client) {
		return
	}
	var request struct {
		Workspace []byte `json:"workspace"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	ref, err := s.store.AdminImportSingleWorkspace(r.Context(), request.Workspace)
	if err != nil {
		writeError(w, http.StatusConflict, "workspace_import_failed")
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

func (s *Server) currentWorkspace(w http.ResponseWriter, r *http.Request, _ Client) {
	response, err := s.store.Current(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace_not_found")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request, client Client) {
	var request SyncRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	response, err := s.store.Sync(r.Context(), client.ActorID, request)
	if err != nil {
		writeError(w, http.StatusConflict, "sync_failed")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createPairing(w http.ResponseWriter, r *http.Request, client Client) {
	if !requireAdmin(w, client) {
		return
	}
	var request struct {
		Role       Role  `json:"role"`
		TTLSeconds int64 `json:"ttl_seconds"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	pairing, err := s.store.CreatePairing(request.Role, time.Duration(request.TTLSeconds)*time.Second)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pairing")
		return
	}
	writeJSON(w, http.StatusCreated, pairing)
}

func (s *Server) revokeClient(w http.ResponseWriter, r *http.Request, client Client) {
	if !requireAdmin(w, client) {
		return
	}
	if err := s.store.RevokeClient(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "client_not_found")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Revoked bool `json:"revoked"`
	}{true})
}

func requireAdmin(w http.ResponseWriter, client Client) bool {
	if client.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "admin_required")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
