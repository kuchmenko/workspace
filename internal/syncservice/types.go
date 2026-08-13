package syncservice

import (
	"encoding/json"
	"time"
)

const ProtocolVersion = 1

type Role string

const (
	RoleClient Role = "client"
	RoleAdmin  Role = "admin"
)

type Client struct {
	ActorID       string `json:"actor_id"`
	DisplayName   string `json:"display_name"`
	Role          Role   `json:"role"`
	Serial        string `json:"-"`
	PublicKeyHash string `json:"-"`
	Active        bool   `json:"active"`
}

type Pairing struct {
	Token     string    `json:"token"`
	Role      Role      `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PairRequest struct {
	AttemptID   string `json:"attempt_id"`
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	CSR         string `json:"csr"`
}

type PairResponse struct {
	ActorID      string `json:"actor_id"`
	Role         Role   `json:"role"`
	CAPEM        string `json:"ca_pem"`
	Certificate  string `json:"certificate_pem"`
	ServiceID    string `json:"service_id"`
	ServiceEpoch string `json:"service_epoch"`
}

type UpgradeRequest struct {
	Version string `json:"version"`
}

type UpdaterRequest struct {
	Version    string `json:"version"`
	BackupPath string `json:"backup_path"`
}

type UpgradeResponse struct {
	Accepted bool   `json:"accepted"`
	Version  string `json:"version"`
	Backup   string `json:"backup"`
}

type StateRef struct {
	ServiceID    string `json:"service_id"`
	ServiceEpoch string `json:"service_epoch"`
	WorkspaceID  string `json:"workspace_id"`
	Revision     int64  `json:"revision"`
	SemanticHash string `json:"semantic_hash"`
}

type SyncRequest struct {
	RequestID        string `json:"request_id"`
	WorkspaceID      string `json:"workspace_id"`
	ServiceID        string `json:"service_id"`
	ServiceEpoch     string `json:"service_epoch"`
	BaseRevision     int64  `json:"base_revision"`
	BaseSemanticHash string `json:"base_semantic_hash"`
	Desired          []byte `json:"desired"`
}

type Conflict struct {
	Path   string          `json:"path"`
	Base   json.RawMessage `json:"base"`
	Local  json.RawMessage `json:"local"`
	Remote json.RawMessage `json:"remote"`
}

type SyncResponse struct {
	State     StateRef   `json:"state"`
	Workspace []byte     `json:"workspace"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
}
