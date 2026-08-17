package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	AccessLocal    = "local"
	AccessAll      = "all"
	AccessSelected = "selected"

	WorkspaceAdmin   = "admin"
	WorkspaceWriter  = "writer"
	WorkspaceReplica = "replica"
)

type AccessPolicy struct {
	Mode        string            `json:"mode"`
	DefaultRole string            `json:"default_role,omitempty"`
	Roles       map[string]string `json:"roles"`
	Denied      []string          `json:"denied,omitempty"`
}

type WorkspaceSummary struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Epoch       int64  `json:"epoch"`
	Role        string `json:"role"`
	Head        string `json:"head"`
}

type Quarantine struct {
	WorkspaceID string `json:"workspace_id"`
	SourceID    string `json:"source_device_id"`
	Head        string `json:"head"`
	Epoch       int64  `json:"epoch"`
	Reason      string `json:"reason"`
	ReceivedAt  string `json:"received_at"`
}

func localPolicy(deviceID string) AccessPolicy {
	return AccessPolicy{Mode: AccessLocal, Roles: map[string]string{deviceID: WorkspaceAdmin}}
}

func normalizePolicy(policy AccessPolicy) (AccessPolicy, error) {
	policy.Mode = strings.TrimSpace(policy.Mode)
	if policy.Roles == nil {
		policy.Roles = map[string]string{}
	}
	policy.Denied = uniqueSorted(policy.Denied)
	if err := validatePolicyMode(&policy); err != nil {
		return AccessPolicy{}, err
	}
	admins, err := validatePolicyRoles(policy)
	if err != nil {
		return AccessPolicy{}, err
	}
	if admins == 0 {
		return AccessPolicy{}, errors.New("workspace must have at least one admin")
	}
	if policy.Mode == AccessLocal && (len(policy.Roles) != 1 || len(policy.Denied) != 0) {
		return AccessPolicy{}, errors.New("local workspace access must contain only one admin")
	}
	return policy, nil
}

func validatePolicyMode(policy *AccessPolicy) error {
	if policy.Mode != AccessLocal && policy.Mode != AccessAll && policy.Mode != AccessSelected {
		return errors.New("workspace access mode must be local, all, or selected")
	}
	if policy.Mode != AccessAll {
		policy.DefaultRole = ""
		return nil
	}
	if policy.DefaultRole != WorkspaceWriter && policy.DefaultRole != WorkspaceReplica {
		return errors.New("shared workspace default role must be writer or replica")
	}
	return nil
}

func validatePolicyRoles(policy AccessPolicy) (int, error) {
	admins := 0
	for deviceID, role := range policy.Roles {
		if strings.TrimSpace(deviceID) == "" || !validWorkspaceRole(role) {
			return 0, errors.New("workspace access contains an invalid device role")
		}
		if role == WorkspaceAdmin {
			admins++
		}
	}
	for _, deviceID := range policy.Denied {
		if policy.Roles[deviceID] != "" {
			return 0, errors.New("workspace device cannot have both a role and a denial")
		}
	}
	return admins, nil
}

func validWorkspaceRole(role string) bool {
	return role == WorkspaceAdmin || role == WorkspaceWriter || role == WorkspaceReplica
}

func (policy AccessPolicy) Role(deviceID string, active bool) string {
	if !active {
		return ""
	}
	for _, deniedID := range policy.Denied {
		if deniedID == deviceID {
			return ""
		}
	}
	if role := policy.Roles[deviceID]; role != "" {
		return role
	}
	if policy.Mode == AccessAll {
		return policy.DefaultRole
	}
	return ""
}

func (store *Store) Access(ctx context.Context, name string) (AccessPolicy, error) {
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return AccessPolicy{}, err
	}
	return store.policyAt(ctx, workspace.Head)
}

func (store *Store) SetAccess(ctx context.Context, name string, policy AccessPolicy) (Workspace, error) {
	policy, err := normalizePolicy(policy)
	if err != nil {
		return Workspace{}, err
	}
	if err = store.validatePolicyDevices(ctx, policy); err != nil {
		return Workspace{}, err
	}
	localActive, _ := store.localNetworkPresence(ctx)
	if err = store.persistAccess(ctx, name, policy, localActive); err != nil {
		return Workspace{}, err
	}
	return store.LoadByName(ctx, name)
}

func (store *Store) persistAccess(ctx context.Context, name string, policy AccessPolicy, localActive bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	base, err := loadAccessBase(ctx, tx, name)
	if err != nil {
		return err
	}
	if err = requireNoAccessConflict(tx, base.workspaceID); err != nil {
		return err
	}
	if base.policy.Role(store.identity.ID(), localActive) != WorkspaceAdmin {
		return errors.New("local device is not a workspace admin")
	}
	if equalPolicy(base.policy, policy) {
		return nil
	}
	revision, epoch, err := store.makeAccessRevision(tx, base, policy)
	if err != nil {
		return err
	}
	if err = insertRevision(tx, revision); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE workspace_protocol SET epoch=?,head_id=? WHERE name=? AND head_id=?`, epoch, revision.ID, name, base.head)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("workspace changed during access update")
	}
	if err = replaceHeads(ctx, tx, base.workspaceID, []string{revision.ID}); err != nil {
		return err
	}
	if err = replaceConflicts(ctx, tx, base.workspaceID, revision.ID, revision.Conflicts); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE name=? AND revision=?`, name, base.revision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("workspace changed during access update")
	}
	return tx.Commit()
}

type accessBase struct {
	workspaceID string
	head        string
	epoch       int64
	revision    int64
	policy      AccessPolicy
}

func loadAccessBase(ctx context.Context, tx *sql.Tx, name string) (accessBase, error) {
	var base accessBase
	err := tx.QueryRowContext(ctx, `SELECT p.workspace_id,p.epoch,p.head_id,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name WHERE p.name=?`, name).Scan(&base.workspaceID, &base.epoch, &base.head, &base.revision)
	if err != nil {
		return base, err
	}
	base.policy, err = policyAtTx(tx, base.head)
	return base, err
}

func (store *Store) makeAccessRevision(tx *sql.Tx, base accessBase, policy AccessPolicy) (Revision, int64, error) {
	epoch := base.epoch
	if policyRestricts(base.policy, policy) {
		epoch++
	}
	snapshot, err := loadRevisionSnapshot(tx, base.head)
	if err != nil {
		return Revision{}, 0, err
	}
	conflicts, err := loadRevisionConflicts(tx, base.head)
	if err != nil {
		return Revision{}, 0, err
	}
	if policySharedWithOtherDevice(policy, store.identity.ID()) {
		if err = validateShareableHistory(tx, base.workspaceID); err != nil {
			return Revision{}, 0, err
		}
	}
	revision, err := makeRevision(base.workspaceID, epoch, "access", []string{base.head}, snapshot, conflicts, policy, store.identity)
	return revision, epoch, err
}

func (store *Store) ListShared(ctx context.Context, peerID string) ([]WorkspaceSummary, error) {
	if err := store.ReconcileNetworkAccess(ctx); err != nil {
		return nil, err
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := store.sharedCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var summaries []WorkspaceSummary
	for _, summary := range candidates {
		workspace, loadErr := store.LoadByName(ctx, summary.Name)
		if loadErr != nil {
			return nil, loadErr
		}
		policy, policyErr := store.authorizationPolicy(ctx, workspace)
		if policyErr != nil {
			return nil, policyErr
		}
		summary.Role = policy.Role(peerID, active[peerID])
		if summary.Role != "" {
			summaries = append(summaries, summary)
		}
	}
	return summaries, nil
}

func (store *Store) sharedCandidates(ctx context.Context) ([]WorkspaceSummary, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT w.name,p.workspace_id,p.epoch,p.head_id FROM workspaces w JOIN workspace_protocol p ON p.name=w.name ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var candidates []WorkspaceSummary
	for rows.Next() {
		var summary WorkspaceSummary
		if err = rows.Scan(&summary.Name, &summary.WorkspaceID, &summary.Epoch, &summary.Head); err != nil {
			return nil, err
		}
		candidates = append(candidates, summary)
	}
	return candidates, rows.Err()
}

func (store *Store) ExportFor(ctx context.Context, name, peerID string) (Bundle, error) {
	if err := store.ReconcileNetworkAccess(ctx); err != nil {
		return Bundle{}, err
	}
	workspace, err := store.LoadByName(ctx, name)
	if err != nil {
		return Bundle{}, err
	}
	policy, err := store.authorizationPolicy(ctx, workspace)
	if err != nil {
		return Bundle{}, err
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return Bundle{}, err
	}
	if policy.Role(peerID, active[peerID]) == "" {
		return Bundle{}, errors.New("peer is not authorized for workspace")
	}
	bundle, err := store.Export(ctx, name)
	if err != nil {
		return Bundle{}, err
	}
	for _, revision := range bundle.Revisions {
		if err = validateShareableSnapshot(revision.Snapshot); err != nil {
			return Bundle{}, err
		}
	}
	return bundle, nil
}

func (store *Store) WorkspaceNameByID(ctx context.Context, workspaceID string) (string, error) {
	var name string
	err := store.db.QueryRowContext(ctx, `SELECT name FROM workspace_protocol WHERE workspace_id=?`, workspaceID).Scan(&name)
	return name, err
}

func (store *Store) policyAt(ctx context.Context, revisionID string) (AccessPolicy, error) {
	var body []byte
	if err := store.db.QueryRowContext(ctx, `SELECT access FROM revisions WHERE id=?`, revisionID).Scan(&body); err != nil {
		return AccessPolicy{}, err
	}
	return decodePolicy(body)
}

func policyAtTx(tx *sql.Tx, revisionID string) (AccessPolicy, error) {
	var body []byte
	if err := tx.QueryRow(`SELECT access FROM revisions WHERE id=?`, revisionID).Scan(&body); err != nil {
		return AccessPolicy{}, err
	}
	return decodePolicy(body)
}

func decodePolicy(body []byte) (AccessPolicy, error) {
	if len(body) == 0 {
		return AccessPolicy{}, errors.New("revision has no workspace access policy")
	}
	var policy AccessPolicy
	if err := json.Unmarshal(body, &policy); err != nil {
		return AccessPolicy{}, err
	}
	return normalizePolicy(policy)
}

func equalPolicy(left, right AccessPolicy) bool {
	leftBody, _ := json.Marshal(left)
	rightBody, _ := json.Marshal(right)
	return string(leftBody) == string(rightBody)
}

func policyRestricts(previous, next AccessPolicy) bool {
	for _, deviceID := range next.Denied {
		if previous.Role(deviceID, true) != "" {
			return true
		}
	}
	return policyRoleDemoted(previous, next) || previous.Mode == AccessAll && next.Mode != AccessAll || previous.Mode == AccessAll && previous.DefaultRole == WorkspaceWriter && next.DefaultRole == WorkspaceReplica
}

func policyRoleDemoted(previous, next AccessPolicy) bool {
	for deviceID, nextRole := range next.Roles {
		if roleDemoted(previous.Role(deviceID, true), nextRole) {
			return true
		}
	}
	for deviceID, role := range previous.Roles {
		if roleDemoted(role, next.Role(deviceID, true)) {
			return true
		}
	}
	return false
}

func roleDemoted(previous, next string) bool {
	return next == "" || previous == WorkspaceAdmin && next != WorkspaceAdmin || previous == WorkspaceWriter && next == WorkspaceReplica
}

func (store *Store) ReconcileNetworkAccess(ctx context.Context) error {
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return err
	}
	analysis, err := analyzeNetwork(bundle)
	if err != nil {
		return err
	}
	if analysis.conflict != nil {
		return ErrNetworkConflict
	}
	networkHead, err := currentCausalNetworkHead(bundle)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var recoveryIDs []string
	if err = store.reconcileNetworkAccessTx(ctx, tx, analysis.state, networkHead, &recoveryIDs); err != nil {
		return err
	}
	if err = store.ratifyRecoveriesTx(ctx, tx, bundle.ID, networkHead, bundle.Epoch, recoveryIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func reconcilePolicy(policy AccessPolicy, active map[string]bool) (AccessPolicy, bool) {
	changed := false
	for deviceID, role := range policy.Roles {
		if active[deviceID] || role == WorkspaceAdmin && !hasOtherActiveAdmin(policy, deviceID, active) {
			continue
		}
		delete(policy.Roles, deviceID)
		changed = true
	}
	if policy.Mode != AccessAll {
		return policy, changed
	}
	for deviceID, isActive := range active {
		if !isActive && policy.Roles[deviceID] == "" && !containsString(policy.Denied, deviceID) {
			policy.Denied = append(policy.Denied, deviceID)
			changed = true
		}
	}
	return policy, changed
}

func (store *Store) reconcileNetworkAccessTx(ctx context.Context, tx *sql.Tx, network NetworkState, networkHead string, recoveryIDs *[]string) error {
	active := make(map[string]bool, len(network.Devices))
	networkAdmin := false
	for _, record := range network.Devices {
		active[record.ID] = record.Active
		if record.ID == store.identity.ID() && record.Active && record.Role == NetworkAdmin {
			networkAdmin = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.name,p.workspace_id,p.epoch,p.head_id,w.revision FROM workspace_protocol p JOIN workspaces w ON w.name=p.name ORDER BY p.name`)
	if err != nil {
		return err
	}
	type candidate struct {
		name, workspaceID, head string
		epoch, revision         int64
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.name, &item.workspaceID, &item.epoch, &item.head, &item.revision); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, item)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range candidates {
		if _, conflicted, conflictErr := accessConflictBase(tx, item.workspaceID); conflictErr != nil {
			return conflictErr
		} else if conflicted {
			continue
		}
		policy, policyErr := policyAtTx(tx, item.head)
		if policyErr != nil {
			return policyErr
		}
		previous := cloneAccessPolicy(policy)
		kind := "access"
		if !hasActiveWorkspaceAdmin(policy, active) {
			if !networkAdmin {
				continue
			}
			policy.Roles[store.identity.ID()] = WorkspaceAdmin
			kind = "access-recovery"
		} else if policy.Role(store.identity.ID(), active[store.identity.ID()]) != WorkspaceAdmin {
			continue
		}
		policy, changed := reconcilePolicy(policy, active)
		if kind == "access-recovery" {
			changed = true
		}
		if !changed {
			continue
		}
		snapshot, loadErr := loadRevisionSnapshot(tx, item.head)
		if loadErr != nil {
			return loadErr
		}
		conflicts, loadErr := loadRevisionConflicts(tx, item.head)
		if loadErr != nil {
			return loadErr
		}
		epoch := item.epoch
		if kind == "access-recovery" || policyRestricts(previous, policy) {
			epoch++
		}
		authorityHead := ""
		if kind == "access-recovery" {
			authorityHead = networkHead
		}
		revision, makeErr := makeRevisionAtNetworkHead(item.workspaceID, epoch, kind, []string{item.head}, snapshot, conflicts, policy, authorityHead, store.identity)
		if makeErr != nil {
			return makeErr
		}
		if makeErr = insertRevision(tx, revision); makeErr != nil {
			return makeErr
		}
		if kind == "access-recovery" {
			*recoveryIDs = append(*recoveryIDs, revision.ID)
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE workspace_protocol SET epoch=?,head_id=? WHERE name=? AND head_id=?`, epoch, revision.ID, item.name, item.head)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("workspace changed during network access reconciliation")
		}
		if updateErr = replaceHeads(ctx, tx, item.workspaceID, []string{revision.ID}); updateErr != nil {
			return updateErr
		}
		if updateErr = replaceConflicts(ctx, tx, item.workspaceID, revision.ID, conflicts); updateErr != nil {
			return updateErr
		}
		result, updateErr = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE name=? AND revision=?`, item.name, item.revision)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("workspace changed during network access reconciliation")
		}
	}
	return nil
}

func hasActiveWorkspaceAdmin(policy AccessPolicy, active map[string]bool) bool {
	for deviceID, role := range policy.Roles {
		if role == WorkspaceAdmin && active[deviceID] {
			return true
		}
	}
	return false
}

func cloneAccessPolicy(policy AccessPolicy) AccessPolicy {
	roles := make(map[string]string, len(policy.Roles))
	for deviceID, role := range policy.Roles {
		roles[deviceID] = role
	}
	policy.Roles = roles
	policy.Denied = append([]string(nil), policy.Denied...)
	return policy
}

func hasOtherActiveAdmin(policy AccessPolicy, excluded string, active map[string]bool) bool {
	for deviceID, role := range policy.Roles {
		if deviceID != excluded && role == WorkspaceAdmin && active[deviceID] {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (store *Store) validatePolicyDevices(ctx context.Context, policy AccessPolicy) error {
	if policy.Mode == AccessLocal && len(policy.Roles) == 1 && policy.Roles[store.identity.ID()] == WorkspaceAdmin {
		return nil
	}
	active, err := store.activeNetworkDevices(ctx)
	if err != nil {
		return errors.New("device network is required before sharing a workspace")
	}
	for deviceID := range policy.Roles {
		if !active[deviceID] {
			return fmt.Errorf("workspace device %s is not active in the device network", deviceID)
		}
	}
	for _, deviceID := range policy.Denied {
		if _, known := active[deviceID]; !known {
			return fmt.Errorf("denied workspace device %s is not known in the device network", deviceID)
		}
	}
	return nil
}

func (store *Store) activeNetworkDevices(ctx context.Context) (map[string]bool, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(state.Devices))
	for _, record := range state.Devices {
		active[record.ID] = record.Active
	}
	return active, nil
}
