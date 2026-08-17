package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/kuchmenko/workspace/internal/device"
)

const (
	NetworkAdmin  = "admin"
	NetworkMember = "member"
)

type NetworkState struct {
	ID      string         `json:"id"`
	Epoch   int64          `json:"epoch"`
	Devices []DeviceRecord `json:"devices"`
}

type DeviceRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey []byte `json:"public_key"`
	Role      string `json:"role"`
	Active    bool   `json:"active"`
}

type NetworkBundle struct {
	ID     string         `json:"id"`
	Epoch  int64          `json:"epoch"`
	Events []NetworkEvent `json:"events"`
}

type NetworkEvent struct {
	ID              string   `json:"id"`
	NetworkID       string   `json:"network_id"`
	Epoch           int64    `json:"epoch"`
	Version         int      `json:"version,omitempty"`
	Parents         []string `json:"parents,omitempty"`
	SelectedParent  string   `json:"selected_parent,omitempty"`
	RecoveryIDs     []string `json:"recovery_ids,omitempty"`
	Action          string   `json:"action"`
	DeviceID        string   `json:"device_id"`
	DeviceName      string   `json:"device_name"`
	DevicePublicKey []byte   `json:"device_public_key"`
	Role            string   `json:"role"`
	SignerID        string   `json:"signer_id"`
	SignerPublicKey []byte   `json:"signer_public_key"`
	Signature       []byte   `json:"signature"`
}

type networkEventCore struct {
	NetworkID       string `json:"network_id"`
	Epoch           int64  `json:"epoch"`
	Action          string `json:"action"`
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	DevicePublicKey []byte `json:"device_public_key"`
	Role            string `json:"role"`
	SignerID        string `json:"signer_id"`
	SignerPublicKey []byte `json:"signer_public_key"`
}

type causalNetworkEventCore struct {
	Version         int      `json:"version"`
	NetworkID       string   `json:"network_id"`
	Epoch           int64    `json:"epoch"`
	Parents         []string `json:"parents"`
	SelectedParent  string   `json:"selected_parent,omitempty"`
	RecoveryIDs     []string `json:"recovery_ids,omitempty"`
	Action          string   `json:"action"`
	DeviceID        string   `json:"device_id"`
	DeviceName      string   `json:"device_name"`
	DevicePublicKey []byte   `json:"device_public_key"`
	Role            string   `json:"role"`
	SignerID        string   `json:"signer_id"`
	SignerPublicKey []byte   `json:"signer_public_key"`
}

func (store *Store) EnsureNetwork(ctx context.Context, name string) (NetworkState, error) {
	state, err := store.Network(ctx)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return NetworkState{}, err
	}
	networkID := uuid.NewString()
	self := DeviceRecord{ID: store.identity.ID(), Name: strings.TrimSpace(name), PublicKey: store.identity.PublicKey(), Role: NetworkAdmin, Active: true}
	event, err := makeNetworkEvent(networkID, 1, "add", self, store.identity)
	if err != nil {
		return NetworkState{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return NetworkState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO networks(id,epoch) VALUES(?,1)`, networkID); err != nil {
		return NetworkState{}, err
	}
	if err = insertNetworkEvent(ctx, tx, event); err != nil {
		return NetworkState{}, err
	}
	if err = tx.Commit(); err != nil {
		return NetworkState{}, err
	}
	return NetworkState{ID: networkID, Epoch: 1, Devices: []DeviceRecord{self}}, nil
}

func (store *Store) Network(ctx context.Context) (NetworkState, error) {
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	analysis, err := analyzeNetwork(bundle)
	return analysis.state, err
}

func (store *Store) ExportNetwork(ctx context.Context) (NetworkBundle, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return NetworkBundle{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var bundle NetworkBundle
	if err = tx.QueryRowContext(ctx, `SELECT id,epoch FROM networks LIMIT 1`).Scan(&bundle.ID, &bundle.Epoch); err != nil {
		return NetworkBundle{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,network_id,epoch,version,parents,selected_parent,recovery_ids,action,device_id,device_name,device_public_key,role,signer_id,signer_public_key,signature FROM network_events WHERE network_id=? ORDER BY epoch,id`, bundle.ID)
	if err != nil {
		return NetworkBundle{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var event NetworkEvent
		var parents, recoveryIDs []byte
		if err = rows.Scan(&event.ID, &event.NetworkID, &event.Epoch, &event.Version, &parents, &event.SelectedParent, &recoveryIDs, &event.Action, &event.DeviceID, &event.DeviceName, &event.DevicePublicKey, &event.Role, &event.SignerID, &event.SignerPublicKey, &event.Signature); err != nil {
			return NetworkBundle{}, err
		}
		if err = json.Unmarshal(parents, &event.Parents); err != nil {
			return NetworkBundle{}, err
		}
		if err = json.Unmarshal(recoveryIDs, &event.RecoveryIDs); err != nil {
			return NetworkBundle{}, err
		}
		bundle.Events = append(bundle.Events, event)
	}
	if err = rows.Err(); err != nil {
		return NetworkBundle{}, err
	}
	if err = rows.Close(); err != nil {
		return NetworkBundle{}, err
	}
	if err = tx.Commit(); err != nil {
		return NetworkBundle{}, err
	}
	return bundle, nil
}

func (store *Store) ImportNetwork(ctx context.Context, bundle NetworkBundle, inviterID string) (NetworkState, error) {
	analysis, err := analyzeNetwork(bundle)
	if err != nil {
		return NetworkState{}, err
	}
	if analysis.conflict != nil {
		return NetworkState{}, ErrNetworkConflict
	}
	state := analysis.state
	inviter, found := findDevice(state.Devices, inviterID)
	if !found || !inviter.Active || inviter.Role != NetworkAdmin {
		return NetworkState{}, errors.New("pairing inviter is not a network admin")
	}
	if err = store.persistNetwork(ctx, bundle, true); err != nil {
		return NetworkState{}, err
	}
	return state, nil
}

func (store *Store) MergeNetwork(ctx context.Context, incoming NetworkBundle) (NetworkState, error) {
	return store.mergeNetwork(ctx, incoming)
}

func (store *Store) MergeNetworkFrom(ctx context.Context, incoming NetworkBundle, _ string) (NetworkState, error) {
	return store.mergeNetwork(ctx, incoming)
}

func (store *Store) mergeNetwork(ctx context.Context, incoming NetworkBundle) (NetworkState, error) {
	local, err := store.ExportNetwork(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	if local.ID != incoming.ID {
		return NetworkState{}, errors.New("peer belongs to another network")
	}
	combined := combineNetworkBundles(local, incoming)
	analysis, err := analyzeNetwork(combined)
	if err != nil {
		return NetworkState{}, err
	}
	if err = store.persistNetwork(ctx, combined, false); err != nil {
		return NetworkState{}, err
	}
	if analysis.conflict != nil {
		return analysis.state, ErrNetworkConflict
	}
	return store.Network(ctx)
}

func combineNetworkBundles(local, incoming NetworkBundle) NetworkBundle {
	events := make(map[string]NetworkEvent, len(local.Events)+len(incoming.Events))
	for _, event := range local.Events {
		events[event.ID] = event
	}
	for _, event := range incoming.Events {
		events[event.ID] = event
	}
	combined := NetworkBundle{ID: local.ID, Epoch: max(local.Epoch, incoming.Epoch), Events: make([]NetworkEvent, 0, len(events))}
	for _, event := range events {
		combined.Events = append(combined.Events, event)
	}
	return combined
}

func (store *Store) persistNetwork(ctx context.Context, bundle NetworkBundle, allowNew bool) error {
	if _, err := analyzeNetwork(bundle); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM networks LIMIT 1`).Scan(&existing)
	if err == nil && existing != bundle.ID {
		return errors.New("device already belongs to another network")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) && !allowNew {
		return errors.New("device does not belong to a network")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO networks(id,epoch) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET epoch=MAX(epoch,excluded.epoch)`, bundle.ID, bundle.Epoch); err != nil {
		return err
	}
	for _, event := range bundle.Events {
		if err = insertNetworkEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	current, err := exportNetworkTx(ctx, tx, bundle.ID)
	if err != nil {
		return err
	}
	analysis, err := analyzeNetwork(current)
	if err != nil {
		return err
	}
	if err = persistNetworkConflict(ctx, tx, analysis); err != nil {
		return err
	}
	if analysis.conflict == nil {
		networkHead, headErr := currentCausalNetworkHead(current)
		if headErr != nil {
			return headErr
		}
		var recoveryIDs []string
		if err = store.reconcileNetworkAccessTx(ctx, tx, analysis.state, networkHead, &recoveryIDs); err != nil {
			return err
		}
		if err = store.ratifyRecoveriesTx(ctx, tx, bundle.ID, networkHead, current.Epoch, recoveryIDs); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if analysis.conflict != nil {
		return ErrNetworkConflict
	}
	return nil
}

func (store *Store) AddNetworkDevice(ctx context.Context, name string, publicKey ed25519.PublicKey, role string) (NetworkState, error) {
	if role != NetworkAdmin && role != NetworkMember {
		return NetworkState{}, errors.New("network role must be admin or member")
	}
	state, err := store.Network(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	self, found := findDevice(state.Devices, store.identity.ID())
	if !found || !self.Active || self.Role != NetworkAdmin {
		return NetworkState{}, errors.New("local device is not a network admin")
	}
	record := DeviceRecord{ID: device.IDForPublicKey(publicKey), Name: strings.TrimSpace(name), PublicKey: append([]byte(nil), publicKey...), Role: role, Active: true}
	if existing, exists := findDevice(state.Devices, record.ID); exists {
		if existing.Active && existing.Name == record.Name && existing.Role == record.Role && string(existing.PublicKey) == string(record.PublicKey) {
			return state, nil
		}
		return NetworkState{}, errors.New("network device already exists with different attributes")
	}
	bundle, err := store.mutableNetworkBundle(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	event, err := makeCausalNetworkEvent(state.ID, state.Epoch+1, "add", record, networkFrontier(bundle.Events), "", store.identity)
	if err != nil {
		return NetworkState{}, err
	}
	if err = store.persistNetworkEvents(ctx, state.ID, event.Epoch, []NetworkEvent{event}); err != nil {
		return NetworkState{}, err
	}
	return store.Network(ctx)
}

func (store *Store) SetNetworkRole(ctx context.Context, deviceID, role string) (NetworkState, error) {
	if role != NetworkAdmin && role != NetworkMember {
		return NetworkState{}, errors.New("network role must be admin or member")
	}
	state, err := store.Network(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	if err = requireLocalNetworkAdmin(state, store.identity.ID()); err != nil {
		return NetworkState{}, err
	}
	target, found := findDevice(state.Devices, deviceID)
	if !found || !target.Active {
		return NetworkState{}, errors.New("network device is not active")
	}
	if target.Role == role {
		return state, nil
	}
	if target.Role == NetworkAdmin && role != NetworkAdmin && activeAdminCount(state.Devices) == 1 {
		return NetworkState{}, errors.New("cannot demote the last network admin")
	}
	target.Role = role
	bundle, err := store.mutableNetworkBundle(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	event, err := makeCausalNetworkEvent(state.ID, state.Epoch+1, "role", target, networkFrontier(bundle.Events), "", store.identity)
	if err != nil {
		return NetworkState{}, err
	}
	if err = store.persistNetworkEvents(ctx, state.ID, event.Epoch, []NetworkEvent{event}); err != nil {
		return NetworkState{}, err
	}
	return store.Network(ctx)
}

func (store *Store) RemoveNetworkDevice(ctx context.Context, deviceID string) (NetworkState, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	if err = requireLocalNetworkAdmin(state, store.identity.ID()); err != nil {
		return NetworkState{}, err
	}
	target, found := findDevice(state.Devices, deviceID)
	if !found || !target.Active {
		return NetworkState{}, errors.New("network device is not active")
	}
	if target.Role == NetworkAdmin && activeAdminCount(state.Devices) == 1 {
		return NetworkState{}, errors.New("cannot remove the last network admin")
	}
	target.Active = false
	bundle, err := store.mutableNetworkBundle(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	event, err := makeCausalNetworkEvent(state.ID, state.Epoch+1, "remove", target, networkFrontier(bundle.Events), "", store.identity)
	if err != nil {
		return NetworkState{}, err
	}
	if err = store.persistNetworkEvents(ctx, state.ID, event.Epoch, []NetworkEvent{event}); err != nil {
		return NetworkState{}, err
	}
	return store.Network(ctx)
}

func makeNetworkEvent(networkID string, epoch int64, action string, record DeviceRecord, signer device.Identity) (NetworkEvent, error) {
	core := networkEventCore{
		NetworkID:       networkID,
		Epoch:           epoch,
		Action:          action,
		DeviceID:        record.ID,
		DeviceName:      record.Name,
		DevicePublicKey: append([]byte(nil), record.PublicKey...),
		Role:            record.Role,
		SignerID:        signer.ID(),
		SignerPublicKey: signer.PublicKey(),
	}
	body, err := json.Marshal(core)
	if err != nil {
		return NetworkEvent{}, err
	}
	digest := sha256.Sum256(body)
	return NetworkEvent{
		ID:              hex.EncodeToString(digest[:]),
		NetworkID:       core.NetworkID,
		Epoch:           core.Epoch,
		Action:          core.Action,
		DeviceID:        core.DeviceID,
		DeviceName:      core.DeviceName,
		DevicePublicKey: core.DevicePublicKey,
		Role:            core.Role,
		SignerID:        core.SignerID,
		SignerPublicKey: core.SignerPublicKey,
		Signature:       signer.Sign(digest[:]),
	}, nil
}

func verifyNetworkEvent(event NetworkEvent) error {
	if event.Version != 0 {
		return verifyCausalNetworkEvent(event)
	}
	if len(event.Parents) != 0 || event.SelectedParent != "" || len(event.RecoveryIDs) != 0 {
		return errors.New("legacy network event contains unsigned causal metadata")
	}
	core := networkEventCore{
		NetworkID:       event.NetworkID,
		Epoch:           event.Epoch,
		Action:          event.Action,
		DeviceID:        event.DeviceID,
		DeviceName:      event.DeviceName,
		DevicePublicKey: event.DevicePublicKey,
		Role:            event.Role,
		SignerID:        event.SignerID,
		SignerPublicKey: event.SignerPublicKey,
	}
	body, err := json.Marshal(core)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if event.ID != hex.EncodeToString(digest[:]) {
		return errors.New("network event content ID does not match body")
	}
	if len(event.DevicePublicKey) != ed25519.PublicKeySize || event.DeviceID != device.IDForPublicKey(event.DevicePublicKey) {
		return errors.New("network event device identity is invalid")
	}
	if len(event.SignerPublicKey) != ed25519.PublicKeySize || event.SignerID != device.IDForPublicKey(event.SignerPublicKey) {
		return errors.New("network event signer identity is invalid")
	}
	if !ed25519.Verify(event.SignerPublicKey, digest[:], event.Signature) {
		return errors.New("network event signature is invalid")
	}
	return nil
}

func materializeLegacyNetwork(bundle NetworkBundle) (NetworkState, error) {
	if bundle.ID == "" || bundle.Epoch < 1 || len(bundle.Events) == 0 {
		return NetworkState{}, errors.New("network bundle is incomplete")
	}
	events := sortedNetworkEvents(bundle.Events)
	genesisIndex, err := validateNetworkEvents(bundle, events)
	if err != nil {
		return NetworkState{}, err
	}
	genesis := events[genesisIndex]
	devices := map[string]DeviceRecord{genesis.DeviceID: {ID: genesis.DeviceID, Name: genesis.DeviceName, PublicKey: append([]byte(nil), genesis.DevicePublicKey...), Role: genesis.Role, Active: true}}
	events = append(events[:genesisIndex], events[genesisIndex+1:]...)
	currentEpoch, err := applyNetworkEvents(events, devices)
	if err != nil {
		return NetworkState{}, err
	}
	if currentEpoch != bundle.Epoch {
		return NetworkState{}, errors.New("network epoch does not match events")
	}
	result := NetworkState{ID: bundle.ID, Epoch: bundle.Epoch, Devices: make([]DeviceRecord, 0, len(devices))}
	for _, record := range devices {
		result.Devices = append(result.Devices, record)
	}
	sort.Slice(result.Devices, func(left, right int) bool { return result.Devices[left].ID < result.Devices[right].ID })
	return result, nil
}

func sortedNetworkEvents(source []NetworkEvent) []NetworkEvent {
	events := append([]NetworkEvent(nil), source...)
	sort.Slice(events, func(left, right int) bool {
		if events[left].Epoch != events[right].Epoch {
			return events[left].Epoch < events[right].Epoch
		}
		if genesisCandidate(events[left]) != genesisCandidate(events[right]) {
			return genesisCandidate(events[left])
		}
		if networkActionPriority(events[left].Action) != networkActionPriority(events[right].Action) {
			return networkActionPriority(events[left].Action) < networkActionPriority(events[right].Action)
		}
		return events[left].ID < events[right].ID
	})
	return events
}

func validateNetworkEvents(bundle NetworkBundle, events []NetworkEvent) (int, error) {
	genesisIndex := -1
	for index, event := range events {
		if event.NetworkID != bundle.ID || event.Epoch < 1 || event.Epoch > bundle.Epoch {
			return -1, errors.New("network event scope is invalid")
		}
		if err := verifyNetworkEvent(event); err != nil {
			return -1, err
		}
		if genesisCandidate(event) {
			if genesisIndex != -1 {
				return -1, errors.New("network contains multiple genesis events")
			}
			genesisIndex = index
		}
	}
	if genesisIndex == -1 {
		return -1, errors.New("network genesis event is missing")
	}
	return genesisIndex, nil
}

func applyNetworkEvents(events []NetworkEvent, devices map[string]DeviceRecord) (int64, error) {
	currentEpoch := int64(1)
	for len(events) > 0 {
		remaining, epoch, progress, err := applyReadyNetworkEvents(events, devices, currentEpoch)
		if err != nil {
			return 0, err
		}
		if !progress {
			return 0, fmt.Errorf("network event signer %s is not an active admin or its target is unavailable", events[0].SignerID)
		}
		events, currentEpoch = remaining, epoch
	}
	return currentEpoch, nil
}

func applyReadyNetworkEvents(events []NetworkEvent, devices map[string]DeviceRecord, currentEpoch int64) ([]NetworkEvent, int64, bool, error) {
	remaining := events[:0]
	progress := false
	for _, event := range events {
		if event.Epoch < currentEpoch {
			progress = true
			continue
		}
		signer, found := devices[event.SignerID]
		ready := event.Epoch <= currentEpoch+1 && (event.Epoch <= currentEpoch || event.Action == "remove") && found && signer.Active && signer.Role == NetworkAdmin && networkTargetReady(event, devices)
		if !ready {
			remaining = append(remaining, event)
			continue
		}
		currentEpoch = max(currentEpoch, event.Epoch)
		if err := applyNetworkEvent(event, devices); err != nil {
			return nil, 0, false, err
		}
		progress = true
	}
	return remaining, currentEpoch, progress, nil
}

func networkTargetReady(event NetworkEvent, devices map[string]DeviceRecord) bool {
	if event.Action == "add" {
		return true
	}
	_, found := devices[event.DeviceID]
	return found
}

func applyNetworkEvent(event NetworkEvent, devices map[string]DeviceRecord) error {
	switch event.Action {
	case "add":
		if event.Role != NetworkAdmin && event.Role != NetworkMember {
			return errors.New("network add role is invalid")
		}
		devices[event.DeviceID] = DeviceRecord{ID: event.DeviceID, Name: event.DeviceName, PublicKey: append([]byte(nil), event.DevicePublicKey...), Role: event.Role, Active: true}
	case "role":
		record, found := devices[event.DeviceID]
		if !found || !record.Active || event.Role != NetworkAdmin && event.Role != NetworkMember {
			return errors.New("network role event is invalid")
		}
		record.Role = event.Role
		devices[event.DeviceID] = record
	case "remove":
		record, found := devices[event.DeviceID]
		if !found {
			return errors.New("network removal target is unknown")
		}
		record.Active = false
		devices[event.DeviceID] = record
	default:
		return errors.New("network event action is invalid")
	}
	return nil
}

func genesisCandidate(event NetworkEvent) bool {
	return event.Action == "add" && event.Epoch == 1 && event.DeviceID == event.SignerID && event.Role == NetworkAdmin
}

func networkActionPriority(action string) int {
	switch action {
	case "add":
		return 0
	case "role":
		return 1
	case "remove":
		return 2
	default:
		return 3
	}
}

func insertNetworkEvent(ctx context.Context, tx *sql.Tx, event NetworkEvent) error {
	if err := verifyNetworkEvent(event); err != nil {
		return err
	}
	parents, err := json.Marshal(event.Parents)
	if err != nil {
		return err
	}
	devicePublicKey := event.DevicePublicKey
	if (event.Action == "resolve" || event.Action == "recover") && devicePublicKey == nil {
		devicePublicKey = []byte{}
	}
	recoveryIDs, err := json.Marshal(event.RecoveryIDs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO network_events(id,network_id,epoch,version,parents,selected_parent,recovery_ids,action,device_id,device_name,device_public_key,role,signer_id,signer_public_key,signature) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.ID, event.NetworkID, event.Epoch, event.Version, parents, event.SelectedParent, recoveryIDs, event.Action, event.DeviceID, event.DeviceName, devicePublicKey, event.Role, event.SignerID, event.SignerPublicKey, event.Signature)
	return err
}

func findDevice(devices []DeviceRecord, id string) (DeviceRecord, bool) {
	for _, record := range devices {
		if record.ID == id {
			return record, true
		}
	}
	return DeviceRecord{}, false
}

func requireLocalNetworkAdmin(state NetworkState, localID string) error {
	self, found := findDevice(state.Devices, localID)
	if !found || !self.Active || self.Role != NetworkAdmin {
		return errors.New("local device is not a network admin")
	}
	return nil
}

func activeAdminCount(devices []DeviceRecord) int {
	count := 0
	for _, record := range devices {
		if record.Active && record.Role == NetworkAdmin {
			count++
		}
	}
	return count
}
