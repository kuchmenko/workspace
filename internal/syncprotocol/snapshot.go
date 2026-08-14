package syncprotocol

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
)

const SnapshotSchemaVersion uint64 = 1

type Role uint64

const (
	RoleReplica Role = iota + 1
	RoleWriter
	RoleAdmin
)

type Member struct {
	NodeID    NodeID `cbor:"1,keyasint"`
	PublicKey []byte `cbor:"2,keyasint"`
	Role      Role   `cbor:"3,keyasint"`
}

type WorkspaceSnapshot struct {
	Schema            uint64           `cbor:"1,keyasint"`
	Registry          RegistrySnapshot `cbor:"2,keyasint"`
	Members           []Member         `cbor:"3,keyasint"`
	AdminThreshold    uint64           `cbor:"4,keyasint"`
	RecoveryPublicKey []byte           `cbor:"5,keyasint"`
}

type RegistrySnapshot struct {
	Version     uint64                     `cbor:"1,keyasint"`
	DefaultView string                     `cbor:"2,keyasint"`
	Groups      map[string]GroupSnapshot   `cbor:"3,keyasint"`
	Projects    map[string]ProjectSnapshot `cbor:"4,keyasint"`
	Aliases     map[string]string          `cbor:"5,keyasint"`
}

type GroupSnapshot struct {
	Description string `cbor:"1,keyasint"`
	Favorite    bool   `cbor:"2,keyasint"`
}

type ProjectSnapshot struct {
	Remote        string                   `cbor:"1,keyasint"`
	Mirrors       map[string]string        `cbor:"2,keyasint"`
	Path          string                   `cbor:"3,keyasint"`
	Status        string                   `cbor:"4,keyasint"`
	Category      string                   `cbor:"5,keyasint"`
	Group         string                   `cbor:"6,keyasint"`
	DefaultBranch string                   `cbor:"7,keyasint"`
	Favorite      bool                     `cbor:"8,keyasint"`
	Branches      []BranchMetadataSnapshot `cbor:"9,keyasint"`
}

type BranchMetadataSnapshot struct {
	Name              string   `cbor:"1,keyasint"`
	Machines          []string `cbor:"2,keyasint"`
	LastActiveMachine string   `cbor:"3,keyasint"`
	LastActiveAt      string   `cbor:"4,keyasint"`
	LastPushedMachine string   `cbor:"5,keyasint"`
	LastPushedAt      string   `cbor:"6,keyasint"`
	CreatedBy         string   `cbor:"7,keyasint"`
	CreatedAt         string   `cbor:"8,keyasint"`
}

func NewWorkspaceSnapshot(workspace *config.Workspace, members []Member, adminThreshold uint64, recoveryPublicKey ed25519.PublicKey) (WorkspaceSnapshot, error) {
	snapshot := WorkspaceSnapshot{
		Schema:            SnapshotSchemaVersion,
		Registry:          registrySnapshot(workspace),
		Members:           append([]Member(nil), members...),
		AdminThreshold:    adminThreshold,
		RecoveryPublicKey: append([]byte(nil), recoveryPublicKey...),
	}
	snapshot.normalize()
	return snapshot, snapshot.Validate()
}

func EncodeWorkspaceSnapshot(snapshot WorkspaceSnapshot) ([]byte, error) {
	snapshot.normalize()
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return encodeMode.Marshal(snapshot)
}

func DecodeWorkspaceSnapshot(data []byte) (WorkspaceSnapshot, error) {
	var snapshot WorkspaceSnapshot
	if len(data) == 0 || len(data) > MaxRevisionCoreBytes {
		return snapshot, fmt.Errorf("workspace snapshot size must be between 1 and %d bytes", MaxRevisionCoreBytes)
	}
	if err := decodeMode.Unmarshal(data, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode workspace snapshot: %w", err)
	}
	snapshot.normalize()
	if err := snapshot.Validate(); err != nil {
		return snapshot, err
	}
	canonical, err := encodeMode.Marshal(snapshot)
	if err != nil {
		return snapshot, err
	}
	if !bytes.Equal(data, canonical) {
		return snapshot, errors.New("workspace snapshot is not deterministic CBOR")
	}
	return snapshot, nil
}

func (snapshot WorkspaceSnapshot) Validate() error {
	if snapshot.Schema != SnapshotSchemaVersion {
		return fmt.Errorf("unsupported snapshot schema %d", snapshot.Schema)
	}
	if len(snapshot.RecoveryPublicKey) != ed25519.PublicKeySize {
		return errors.New("invalid recovery public key")
	}
	adminCount := uint64(0)
	for index, member := range snapshot.Members {
		if len(member.PublicKey) != ed25519.PublicKeySize {
			return errors.New("invalid member public key")
		}
		nodeID, err := NodeIDFor(ed25519.PublicKey(member.PublicKey))
		if err != nil || nodeID != member.NodeID {
			return errors.New("member node ID does not match public key")
		}
		if member.Role < RoleReplica || member.Role > RoleAdmin {
			return errors.New("invalid member role")
		}
		if index > 0 && bytes.Compare(snapshot.Members[index-1].NodeID[:], member.NodeID[:]) >= 0 {
			return errors.New("members must be sorted and unique")
		}
		if member.Role == RoleAdmin {
			adminCount++
		}
	}
	if snapshot.AdminThreshold == 0 || snapshot.AdminThreshold > adminCount {
		return errors.New("admin threshold exceeds active administrators")
	}
	workspace := snapshot.Registry.Workspace()
	if issues := workspace.Validate(); len(issues) != 0 {
		return fmt.Errorf("invalid registry snapshot: %v", issues)
	}
	return nil
}

func (snapshot WorkspaceSnapshot) Workspace(root string) *config.Workspace {
	workspace := snapshot.Registry.Workspace()
	workspace.RestoreRoot(root)
	return workspace
}

func (snapshot *WorkspaceSnapshot) normalize() {
	if snapshot.Members == nil {
		snapshot.Members = []Member{}
	}
	for index := range snapshot.Members {
		snapshot.Members[index].PublicKey = append([]byte(nil), snapshot.Members[index].PublicKey...)
	}
	sort.Slice(snapshot.Members, func(left, right int) bool {
		return bytes.Compare(snapshot.Members[left].NodeID[:], snapshot.Members[right].NodeID[:]) < 0
	})
	snapshot.RecoveryPublicKey = append([]byte(nil), snapshot.RecoveryPublicKey...)
	snapshot.Registry.normalize()
}

func registrySnapshot(workspace *config.Workspace) RegistrySnapshot {
	registry := RegistrySnapshot{
		Version:     uint64(workspace.Meta.Version),
		DefaultView: workspace.Agent.DefaultView,
		Groups:      make(map[string]GroupSnapshot, len(workspace.Groups)),
		Projects:    make(map[string]ProjectSnapshot, len(workspace.Projects)),
		Aliases:     make(map[string]string, len(workspace.Aliases)),
	}
	for name, group := range workspace.Groups {
		registry.Groups[name] = GroupSnapshot{Description: group.Description, Favorite: group.Favorite}
	}
	for name, project := range workspace.Projects {
		converted := ProjectSnapshot{
			Remote:        project.Remote,
			Mirrors:       make(map[string]string, len(project.Mirrors)),
			Path:          project.Path,
			Status:        string(project.Status),
			Category:      string(project.Category),
			Group:         project.Group,
			DefaultBranch: project.DefaultBranch,
			Favorite:      project.Favorite,
			Branches:      make([]BranchMetadataSnapshot, 0, len(project.Branches)),
		}
		for mirror, remote := range project.Mirrors {
			converted.Mirrors[mirror] = remote
		}
		for _, branch := range project.Branches {
			converted.Branches = append(converted.Branches, BranchMetadataSnapshot{
				Name:              branch.Name,
				Machines:          append([]string(nil), branch.Machines...),
				LastActiveMachine: branch.LastActiveMachine,
				LastActiveAt:      branch.LastActiveAt,
				LastPushedMachine: branch.LastPushedMachine,
				LastPushedAt:      branch.LastPushedAt,
				CreatedBy:         branch.CreatedBy,
				CreatedAt:         branch.CreatedAt,
			})
		}
		registry.Projects[name] = converted
	}
	for name, target := range workspace.Aliases {
		registry.Aliases[name] = target
	}
	registry.normalize()
	return registry
}

func (registry *RegistrySnapshot) normalize() {
	if registry.Groups == nil {
		registry.Groups = map[string]GroupSnapshot{}
	}
	if registry.Projects == nil {
		registry.Projects = map[string]ProjectSnapshot{}
	}
	if registry.Aliases == nil {
		registry.Aliases = map[string]string{}
	}
	for name, project := range registry.Projects {
		if project.Mirrors == nil {
			project.Mirrors = map[string]string{}
		}
		if project.Branches == nil {
			project.Branches = []BranchMetadataSnapshot{}
		}
		for index := range project.Branches {
			if project.Branches[index].Machines == nil {
				project.Branches[index].Machines = []string{}
			}
			sort.Strings(project.Branches[index].Machines)
			project.Branches[index].Machines = uniqueStrings(project.Branches[index].Machines)
		}
		sort.Slice(project.Branches, func(left, right int) bool {
			return project.Branches[left].Name < project.Branches[right].Name
		})
		registry.Projects[name] = project
	}
}

func (registry RegistrySnapshot) Workspace() *config.Workspace {
	workspace := &config.Workspace{
		Meta:     config.Meta{Version: int(registry.Version)},
		Agent:    config.AgentConfig{DefaultView: registry.DefaultView},
		Groups:   make(map[string]config.Group, len(registry.Groups)),
		Projects: make(map[string]config.Project, len(registry.Projects)),
		Aliases:  make(map[string]string, len(registry.Aliases)),
	}
	for name, group := range registry.Groups {
		workspace.Groups[name] = config.Group{Description: group.Description, Favorite: group.Favorite}
	}
	for name, project := range registry.Projects {
		converted := config.Project{
			Remote:        project.Remote,
			Mirrors:       make(map[string]string, len(project.Mirrors)),
			Path:          project.Path,
			Status:        config.Status(project.Status),
			Category:      config.Category(project.Category),
			Group:         project.Group,
			DefaultBranch: project.DefaultBranch,
			Favorite:      project.Favorite,
			Branches:      make([]config.BranchMeta, 0, len(project.Branches)),
		}
		for mirror, remote := range project.Mirrors {
			converted.Mirrors[mirror] = remote
		}
		for _, branch := range project.Branches {
			converted.Branches = append(converted.Branches, config.BranchMeta{
				Name:              branch.Name,
				Machines:          append([]string(nil), branch.Machines...),
				LastActiveMachine: branch.LastActiveMachine,
				LastActiveAt:      branch.LastActiveAt,
				LastPushedMachine: branch.LastPushedMachine,
				LastPushedAt:      branch.LastPushedAt,
				CreatedBy:         branch.CreatedBy,
				CreatedAt:         branch.CreatedAt,
			})
		}
		workspace.Projects[name] = converted
	}
	for name, target := range registry.Aliases {
		workspace.Aliases[name] = target
	}
	return workspace
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
