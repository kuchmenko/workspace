package syncclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"

	"github.com/kuchmenko/workspace/internal/syncservice"
)

const maxResponseBytes = 8 << 20

type IdentityError struct{ WantServiceID, WantEpoch, GotServiceID, GotEpoch string }

func (e *IdentityError) Error() string { return "sync service identity mismatch" }

type StatusResponse struct {
	ServiceID       string `json:"service_id"`
	ServiceEpoch    string `json:"service_epoch"`
	ProtocolVersion int    `json:"protocol_version"`
	ActorID         string `json:"actor_id"`
	Role            string `json:"role"`
	Version         string `json:"version"`
}

func (c *Client) Endpoint() string { return strings.TrimRight(c.credentials.Endpoint, "/") }

func (c *Client) Upgrade(ctx context.Context, version string) (syncservice.UpgradeResponse, error) {
	var response syncservice.UpgradeResponse
	err := jsonCall(ctx, c.http, http.MethodPost, c.Endpoint()+"/v1/admin/upgrade", syncservice.UpgradeRequest{Version: version}, &response)
	return response, err
}

type ImportRequest struct {
	Workspace []byte `json:"workspace"`
}
type PairingRequest struct {
	Role       string `json:"role"`
	TTLSeconds int64  `json:"ttl_seconds"`
}
type PairingResult struct {
	Token     string `json:"token"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
}

type Client struct {
	credentials Credentials
	http        *http.Client
}

func New(credentials Credentials) (*Client, error) {
	httpClient, err := credentials.HTTPClient()
	if err != nil {
		return nil, err
	}
	return &Client{credentials: credentials, http: httpClient}, nil
}

func (c *Client) verify(serviceID, epoch string) error {
	if serviceID != c.credentials.ServiceID || epoch != c.credentials.ServiceEpoch {
		return &IdentityError{WantServiceID: c.credentials.ServiceID, WantEpoch: c.credentials.ServiceEpoch, GotServiceID: serviceID, GotEpoch: epoch}
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var response StatusResponse
	err := jsonCall(ctx, c.http, http.MethodGet, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/status", nil, &response)
	if err == nil {
		err = c.verify(response.ServiceID, response.ServiceEpoch)
	}
	return response, err
}

func (c *Client) Current(ctx context.Context, workspaceID string) (syncservice.SyncResponse, error) {
	var response syncservice.SyncResponse
	err := jsonCall(ctx, c.http, http.MethodGet, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/workspaces/"+workspaceID, nil, &response)
	if err == nil {
		err = c.verify(response.State.ServiceID, response.State.ServiceEpoch)
	}
	if err == nil {
		err = verifyResponse(response, workspaceID, 0)
	}
	return response, err
}

func (c *Client) Import(ctx context.Context, workspace []byte) (syncservice.SyncResponse, error) {
	_, canonical, _, err := syncservice.Canonicalize(workspace)
	if err != nil {
		return syncservice.SyncResponse{}, err
	}
	var ref syncservice.StateRef
	err = jsonCall(ctx, c.http, http.MethodPost, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/workspaces/import", ImportRequest{Workspace: canonical}, &ref)
	if err == nil {
		err = c.verify(ref.ServiceID, ref.ServiceEpoch)
	}
	return syncservice.SyncResponse{State: ref, Workspace: canonical}, err
}

func (c *Client) CreatePairing(ctx context.Context, role string) (string, error) {
	var response PairingResult
	err := jsonCall(ctx, c.http, http.MethodPost, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/admin/pairings", PairingRequest{Role: role, TTLSeconds: 600}, &response)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(c.credentials.CAPEM)
	if block == nil {
		return "", errors.New("invalid CA PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	return (PairingCode{Endpoint: strings.TrimRight(c.credentials.Endpoint, "/"), ServiceID: c.credentials.ServiceID, CAFingerprint: hex.EncodeToString(sum[:]), Token: response.Token}).String(), nil
}

func (c *Client) Revoke(ctx context.Context, actorID string) error {
	var response struct {
		Revoked bool `json:"revoked"`
	}
	return jsonCall(ctx, c.http, http.MethodPost, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/admin/clients/"+actorID+"/revoke", nil, &response)
}

func (c *Client) sendSync(ctx context.Context, requestJSON []byte) (syncservice.SyncResponse, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.credentials.Endpoint, "/")+"/v1/sync", strings.NewReader(string(requestJSON)))
	if err != nil {
		return syncservice.SyncResponse{}, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return syncservice.SyncResponse{}, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return syncservice.SyncResponse{}, nil, errors.New(response.Status)
	}
	return decodeSyncResponse(response)
}
