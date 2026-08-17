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

	"github.com/kuchmenko/workspace/internal/device"
)

var ErrNetworkConflict = errors.New("network has an unresolved membership conflict")

type NetworkConflict struct {
	ID        string         `json:"id"`
	NetworkID string         `json:"network_id"`
	Base      string         `json:"base"`
	Heads     []NetworkEvent `json:"heads"`
}

type networkAnalysis struct {
	state    NetworkState
	conflict *NetworkConflict
}

type networkHistory struct {
	legacy   []NetworkEvent
	causal   []NetworkEvent
	maxEpoch int64
}

const maxNetworkBundleBytes = 16 << 20

func analyzeNetwork(bundle NetworkBundle) (networkAnalysis, error) {
	if len(bundle.Events) > 10000 {
		return networkAnalysis{}, errors.New("network event limit exceeded")
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		return networkAnalysis{}, err
	}
	if len(body) >= maxNetworkBundleBytes {
		return networkAnalysis{}, errors.New("network size limit exceeded")
	}
	history, err := validateNetworkHistory(bundle)
	if err != nil {
		return networkAnalysis{}, err
	}
	legacyEpoch := int64(0)
	for _, event := range history.legacy {
		legacyEpoch = max(legacyEpoch, event.Epoch)
	}
	base, err := materializeLegacyNetwork(NetworkBundle{ID: bundle.ID, Epoch: legacyEpoch, Events: history.legacy})
	if err != nil {
		return networkAnalysis{}, err
	}
	if len(history.causal) == 0 {
		return networkAnalysis{state: base}, nil
	}
	states, events, children, err := materializeCausalNetwork(history.causal, base, networkEventIDs(history.legacy))
	if err != nil {
		return networkAnalysis{}, err
	}
	return analyzeNetworkHeads(bundle.ID, base, states, events, children)
}

func validateNetworkHistory(bundle NetworkBundle) (networkHistory, error) {
	history := networkHistory{legacy: make([]NetworkEvent, 0, len(bundle.Events)), causal: make([]NetworkEvent, 0, len(bundle.Events))}
	all := make(map[string]NetworkEvent, len(bundle.Events))
	for _, event := range bundle.Events {
		if event.NetworkID != bundle.ID || event.Epoch < 1 || event.Epoch > bundle.Epoch {
			return networkHistory{}, errors.New("network event scope is invalid")
		}
		if _, found := all[event.ID]; found {
			return networkHistory{}, errors.New("network contains duplicate events")
		}
		if err := verifyNetworkEvent(event); err != nil {
			return networkHistory{}, err
		}
		all[event.ID] = event
		history.maxEpoch = max(history.maxEpoch, event.Epoch)
		if event.Version == 0 {
			history.legacy = append(history.legacy, event)
		} else {
			history.causal = append(history.causal, event)
		}
	}
	if bundle.ID == "" || len(history.legacy) == 0 {
		return networkHistory{}, errors.New("network bundle is incomplete")
	}
	if bundle.Epoch != history.maxEpoch {
		return networkHistory{}, fmt.Errorf("network epoch %d does not match causal history %d", bundle.Epoch, history.maxEpoch)
	}
	return history, nil
}

func materializeCausalNetwork(causal []NetworkEvent, base NetworkState, legacyIDs []string) (map[string]NetworkState, map[string]NetworkEvent, map[string]bool, error) {
	states := make(map[string]NetworkState, len(causal))
	events := make(map[string]NetworkEvent, len(causal))
	children := make(map[string]bool, len(causal))
	sort.Slice(causal, func(i, j int) bool {
		if causal[i].Epoch != causal[j].Epoch {
			return causal[i].Epoch < causal[j].Epoch
		}
		return causal[i].ID < causal[j].ID
	})
	remaining := append([]NetworkEvent(nil), causal...)
	for len(remaining) > 0 {
		next, progress, err := applyCausalNetworkPass(remaining, base, legacyIDs, states, events, children)
		if err != nil {
			return nil, nil, nil, err
		}
		if !progress {
			return nil, nil, nil, errors.New("network causal history is incomplete or cyclic")
		}
		remaining = next
	}
	return states, events, children, nil
}

func applyCausalNetworkPass(remaining []NetworkEvent, base NetworkState, legacyIDs []string, states map[string]NetworkState, events map[string]NetworkEvent, children map[string]bool) ([]NetworkEvent, bool, error) {
	progress := false
	next := remaining[:0]
	for _, event := range remaining {
		state, ready, err := applyCausalNetworkEvent(event, base, legacyIDs, states, events)
		if err != nil {
			return nil, false, err
		}
		if !ready {
			next = append(next, event)
			continue
		}
		states[event.ID] = state
		events[event.ID] = event
		for _, parent := range event.Parents {
			if _, found := states[parent]; found {
				children[parent] = true
			}
		}
		progress = true
	}
	return next, progress, nil
}

func analyzeNetworkHeads(networkID string, base NetworkState, states map[string]NetworkState, events map[string]NetworkEvent, children map[string]bool) (networkAnalysis, error) {
	var heads []string
	for id := range states {
		if !children[id] {
			heads = append(heads, id)
		}
	}
	sort.Strings(heads)
	if len(heads) == 1 {
		return networkAnalysis{state: states[heads[0]]}, nil
	}
	baseID, conflictState := commonNetworkAncestor(heads, states, events, base)
	if !equalNetworkResolutionSets(heads, baseID, events) {
		return networkAnalysis{}, errors.New("stale network branch cannot reopen a resolved membership conflict")
	}
	conflict := NetworkConflict{NetworkID: networkID, Base: baseID}
	for _, id := range heads {
		conflict.Heads = append(conflict.Heads, events[id])
	}
	conflict.ID = networkConflictID(networkID, baseID, heads)
	return networkAnalysis{state: conflictState, conflict: &conflict}, nil
}

func applyCausalNetworkEvent(event NetworkEvent, legacyBase NetworkState, legacyIDs []string, states map[string]NetworkState, events map[string]NetworkEvent) (NetworkState, bool, error) {
	if event.Version != 1 || len(event.Parents) == 0 || len(event.Parents) > 10000 || !sort.StringsAreSorted(event.Parents) || containsDuplicate(event.Parents) {
		return NetworkState{}, false, errors.New("network event causal metadata is invalid")
	}
	if event.Action == "recover" {
		return applyNetworkRecoveryEvent(event, states)
	}
	if event.Action != "resolve" {
		return applyNetworkChangeEvent(event, legacyBase, legacyIDs, states)
	}
	return applyNetworkResolutionEvent(event, legacyBase, states, events)
}

func applyNetworkRecoveryEvent(event NetworkEvent, states map[string]NetworkState) (NetworkState, bool, error) {
	if event.SelectedParent != "" || len(event.Parents) != 1 || len(event.RecoveryIDs) == 0 || len(event.RecoveryIDs) > maxRevisionItems || !sort.StringsAreSorted(event.RecoveryIDs) || containsDuplicate(event.RecoveryIDs) || event.DeviceID != "" || event.DeviceName != "" || len(event.DevicePublicKey) != 0 || event.Role != "" {
		return NetworkState{}, false, errors.New("network recovery ratification is invalid")
	}
	parent, found := states[event.Parents[0]]
	if !found {
		return NetworkState{}, false, nil
	}
	if event.Epoch != parent.Epoch+1 {
		return NetworkState{}, false, errors.New("network recovery epoch does not follow its parent")
	}
	if err := requireNetworkEventAdmin(parent, event); err != nil {
		return NetworkState{}, false, err
	}
	result := cloneNetworkState(parent)
	result.Epoch = event.Epoch
	return result, true, nil
}

func applyNetworkChangeEvent(event NetworkEvent, legacyBase NetworkState, legacyIDs []string, states map[string]NetworkState) (NetworkState, bool, error) {
	if len(event.RecoveryIDs) != 0 {
		return NetworkState{}, false, errors.New("network membership change contains recovery metadata")
	}
	if event.SelectedParent != "" {
		return NetworkState{}, false, errors.New("network change cannot select a resolution parent")
	}
	parent, ready, err := networkChangeParent(event, legacyBase, legacyIDs, states)
	if err != nil || !ready {
		return NetworkState{}, ready, err
	}
	if event.Epoch != parent.Epoch+1 {
		return NetworkState{}, false, errors.New("network event epoch does not follow its parent")
	}
	if err = requireNetworkEventAdmin(parent, event); err != nil {
		return NetworkState{}, false, err
	}
	result := cloneNetworkState(parent)
	result.Epoch = event.Epoch
	devices := deviceMap(result.Devices)
	if err = applyNetworkEvent(event, devices); err != nil {
		return NetworkState{}, false, err
	}
	result.Devices = sortedDevices(devices)
	if activeAdminCount(result.Devices) == 0 {
		return NetworkState{}, false, errors.New("network change removes the last active admin")
	}
	return result, true, nil
}

func networkChangeParent(event NetworkEvent, legacyBase NetworkState, legacyIDs []string, states map[string]NetworkState) (NetworkState, bool, error) {
	if equalStrings(event.Parents, legacyIDs) {
		return legacyBase, true, nil
	}
	if len(event.Parents) != 1 {
		return NetworkState{}, false, errors.New("network change must descend from one causal frontier")
	}
	parent, found := states[event.Parents[0]]
	return parent, found, nil
}

func applyNetworkResolutionEvent(event NetworkEvent, legacyBase NetworkState, states map[string]NetworkState, events map[string]NetworkEvent) (NetworkState, bool, error) {
	if !validNetworkResolutionShape(event) {
		return NetworkState{}, false, errors.New("network resolution is invalid")
	}
	maxEpoch, ready := networkParentEpoch(event.Parents, states)
	if !ready {
		return NetworkState{}, false, nil
	}
	baseID, authorizationState := commonNetworkAncestor(event.Parents, states, events, legacyBase)
	if !equalNetworkResolutionSets(event.Parents, baseID, events) {
		return NetworkState{}, false, errors.New("network resolution cannot graft a stale membership branch")
	}
	if err := requireNetworkEventAdmin(authorizationState, event); err != nil {
		return NetworkState{}, false, err
	}
	result := cloneNetworkState(states[event.SelectedParent])
	if event.Epoch != maxEpoch+1 || activeAdminCount(result.Devices) == 0 {
		return NetworkState{}, false, errors.New("network resolution epoch or selected state is invalid")
	}
	result.Epoch = event.Epoch
	return result, true, nil
}

func validNetworkResolutionShape(event NetworkEvent) bool {
	return len(event.Parents) >= 2 && event.SelectedParent != "" && len(event.RecoveryIDs) == 0 && containsString(event.Parents, event.SelectedParent) && event.DeviceID == "" && event.DeviceName == "" && len(event.DevicePublicKey) == 0 && event.Role == ""
}

func networkParentEpoch(parents []string, states map[string]NetworkState) (int64, bool) {
	var epoch int64
	for _, parentID := range parents {
		parent, found := states[parentID]
		if !found {
			return 0, false
		}
		epoch = max(epoch, parent.Epoch)
	}
	return epoch, true
}

func commonNetworkAncestor(heads []string, states map[string]NetworkState, events map[string]NetworkEvent, legacy NetworkState) (string, NetworkState) {
	common := networkAncestors(heads[0], events)
	for _, head := range heads[1:] {
		other := networkAncestors(head, events)
		for id := range common {
			if !other[id] {
				delete(common, id)
			}
		}
	}
	best := ""
	for id := range common {
		if best == "" || states[id].Epoch > states[best].Epoch || states[id].Epoch == states[best].Epoch && id < best {
			best = id
		}
	}
	if best == "" {
		return "legacy", legacy
	}
	return best, states[best]
}

func networkAncestors(head string, events map[string]NetworkEvent) map[string]bool {
	result := map[string]bool{}
	queue := []string{head}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if result[id] {
			continue
		}
		result[id] = true
		for _, parent := range events[id].Parents {
			if _, found := events[parent]; found {
				queue = append(queue, parent)
			}
		}
	}
	return result
}

func networkResolutionIDsBetween(head, base string, events map[string]NetworkEvent) map[string]bool {
	queue := []string{head}
	seen := map[string]bool{}
	resolutions := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == base || seen[id] {
			continue
		}
		seen[id] = true
		event := events[id]
		if event.Action == "resolve" {
			resolutions[id] = true
		}
		queue = append(queue, event.Parents...)
	}
	return resolutions
}

func equalNetworkResolutionSets(heads []string, base string, events map[string]NetworkEvent) bool {
	if len(heads) < 2 {
		return true
	}
	expected := networkResolutionIDsBetween(heads[0], base, events)
	for _, head := range heads[1:] {
		actual := networkResolutionIDsBetween(head, base, events)
		if len(actual) != len(expected) {
			return false
		}
		for id := range expected {
			if !actual[id] {
				return false
			}
		}
	}
	return true
}

func currentCausalNetworkHead(bundle NetworkBundle) (string, error) {
	events := map[string]NetworkEvent{}
	children := map[string]bool{}
	for _, event := range bundle.Events {
		if event.Version == 0 {
			continue
		}
		events[event.ID] = event
		for _, parent := range event.Parents {
			children[parent] = true
		}
	}
	var heads []string
	for id := range events {
		if !children[id] {
			heads = append(heads, id)
		}
	}
	if len(heads) == 0 {
		return "", nil
	}
	if len(heads) != 1 {
		return "", errors.New("network does not have one causal head")
	}
	return heads[0], nil
}

func networkHeadSelectedBy(bundle NetworkBundle, current, historical string) bool {
	events := make(map[string]NetworkEvent, len(bundle.Events))
	for _, event := range bundle.Events {
		events[event.ID] = event
	}
	for current != "" {
		if current == historical {
			return true
		}
		event, found := events[current]
		if !found || event.Version == 0 {
			return false
		}
		if event.Action == "resolve" {
			current = event.SelectedParent
		} else if len(event.Parents) == 1 {
			current = event.Parents[0]
		} else {
			return false
		}
	}
	return false
}

func networkBundleAtHead(bundle NetworkBundle, head string) (NetworkBundle, error) {
	byID := make(map[string]NetworkEvent, len(bundle.Events))
	for _, event := range bundle.Events {
		byID[event.ID] = event
	}
	wanted := networkAncestors(head, byID)
	result := NetworkBundle{ID: bundle.ID}
	for _, event := range bundle.Events {
		if event.Version == 0 || wanted[event.ID] {
			result.Events = append(result.Events, event)
			result.Epoch = max(result.Epoch, event.Epoch)
		}
	}
	if !wanted[head] {
		return NetworkBundle{}, errors.New("workspace recovery references an unknown network head")
	}
	return result, nil
}

func (store *Store) NetworkConflict(ctx context.Context) (NetworkConflict, error) {
	var conflict NetworkConflict
	var heads []byte
	err := store.db.QueryRowContext(ctx, `SELECT network_id,conflict_id,base_event_id,head_ids FROM network_conflicts LIMIT 1`).Scan(&conflict.NetworkID, &conflict.ID, &conflict.Base, &heads)
	if err != nil {
		return NetworkConflict{}, err
	}
	var ids []string
	if err = json.Unmarshal(heads, &ids); err != nil {
		return NetworkConflict{}, err
	}
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return NetworkConflict{}, err
	}
	byID := make(map[string]NetworkEvent, len(bundle.Events))
	for _, event := range bundle.Events {
		byID[event.ID] = event
	}
	for _, id := range ids {
		conflict.Heads = append(conflict.Heads, byID[id])
	}
	return conflict, nil
}

func (store *Store) ResolveNetworkConflict(ctx context.Context, conflictID, selectedHead string) (NetworkState, error) {
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return NetworkState{}, err
	}
	analysis, err := analyzeNetwork(bundle)
	if err != nil {
		return NetworkState{}, err
	}
	if analysis.conflict == nil || analysis.conflict.ID != conflictID {
		return NetworkState{}, errors.New("network conflict changed")
	}
	heads := make([]string, 0, len(analysis.conflict.Heads))
	for _, event := range analysis.conflict.Heads {
		heads = append(heads, event.ID)
	}
	if !containsString(heads, selectedHead) {
		return NetworkState{}, errors.New("selected event is not a current network conflict head")
	}
	if err = requireLocalNetworkAdmin(analysis.state, store.identity.ID()); err != nil {
		return NetworkState{}, err
	}
	epoch := bundle.Epoch + 1
	event, err := makeCausalNetworkEvent(bundle.ID, epoch, "resolve", DeviceRecord{}, heads, selectedHead, store.identity)
	if err != nil {
		return NetworkState{}, err
	}
	if err = store.persistNetworkEvents(ctx, bundle.ID, epoch, []NetworkEvent{event}); err != nil {
		return NetworkState{}, err
	}
	return store.Network(ctx)
}

func persistNetworkConflict(ctx context.Context, tx *sql.Tx, analysis networkAnalysis) error {
	if analysis.conflict == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM network_conflicts WHERE network_id=?`, analysis.state.ID)
		return err
	}
	heads := make([]string, 0, len(analysis.conflict.Heads))
	for _, event := range analysis.conflict.Heads {
		heads = append(heads, event.ID)
	}
	body, _ := json.Marshal(heads)
	_, err := tx.ExecContext(ctx, `INSERT INTO network_conflicts(network_id,conflict_id,base_event_id,head_ids) VALUES(?,?,?,?) ON CONFLICT(network_id) DO UPDATE SET conflict_id=excluded.conflict_id,base_event_id=excluded.base_event_id,head_ids=excluded.head_ids`, analysis.state.ID, analysis.conflict.ID, analysis.conflict.Base, body)
	return err
}

func networkConflictID(networkID, base string, heads []string) string {
	body, _ := json.Marshal(struct {
		Network string   `json:"network"`
		Base    string   `json:"base"`
		Heads   []string `json:"heads"`
	}{networkID, base, heads})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func makeCausalNetworkEvent(networkID string, epoch int64, action string, record DeviceRecord, parents []string, selected string, signer device.Identity) (NetworkEvent, error) {
	parents = append([]string(nil), parents...)
	sort.Strings(parents)
	if record.PublicKey == nil {
		record.PublicKey = []byte{}
	}
	core := causalNetworkEventCore{Version: 1, NetworkID: networkID, Epoch: epoch, Parents: parents, SelectedParent: selected, Action: action, DeviceID: record.ID, DeviceName: record.Name, DevicePublicKey: append([]byte(nil), record.PublicKey...), Role: record.Role, SignerID: signer.ID(), SignerPublicKey: signer.PublicKey()}
	body, err := json.Marshal(core)
	if err != nil {
		return NetworkEvent{}, err
	}
	digest := sha256.Sum256(body)
	return NetworkEvent{ID: hex.EncodeToString(digest[:]), NetworkID: networkID, Epoch: epoch, Version: 1, Parents: parents, SelectedParent: selected, Action: action, DeviceID: record.ID, DeviceName: record.Name, DevicePublicKey: record.PublicKey, Role: record.Role, SignerID: signer.ID(), SignerPublicKey: signer.PublicKey(), Signature: signer.Sign(digest[:])}, nil
}

func makeNetworkRecoveryEvent(networkID string, epoch int64, parent string, recoveryIDs []string, signer device.Identity) (NetworkEvent, error) {
	recoveryIDs = append([]string(nil), recoveryIDs...)
	sort.Strings(recoveryIDs)
	core := causalNetworkEventCore{Version: 1, NetworkID: networkID, Epoch: epoch, Parents: []string{parent}, RecoveryIDs: recoveryIDs, Action: "recover", SignerID: signer.ID(), SignerPublicKey: signer.PublicKey()}
	body, err := json.Marshal(core)
	if err != nil {
		return NetworkEvent{}, err
	}
	digest := sha256.Sum256(body)
	return NetworkEvent{ID: hex.EncodeToString(digest[:]), NetworkID: networkID, Epoch: epoch, Version: 1, Parents: []string{parent}, RecoveryIDs: recoveryIDs, Action: "recover", SignerID: signer.ID(), SignerPublicKey: signer.PublicKey(), Signature: signer.Sign(digest[:])}, nil
}

func (store *Store) ratifyRecoveriesTx(ctx context.Context, tx *sql.Tx, networkID, parent string, epoch int64, recoveryIDs []string) error {
	if len(recoveryIDs) == 0 {
		return nil
	}
	event, err := makeNetworkRecoveryEvent(networkID, epoch+1, parent, recoveryIDs, store.identity)
	if err != nil {
		return err
	}
	if err = insertNetworkEvent(ctx, tx, event); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE networks SET epoch=? WHERE id=? AND epoch=?`, event.Epoch, networkID, epoch)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("network changed during workspace recovery ratification")
	}
	return nil
}

func verifyCausalNetworkEvent(event NetworkEvent) error {
	if event.Version != 1 {
		return errors.New("network event version is unsupported")
	}
	publicKey := event.DevicePublicKey
	if event.Action == "resolve" || event.Action == "recover" {
		publicKey = nil
	}
	core := causalNetworkEventCore{Version: event.Version, NetworkID: event.NetworkID, Epoch: event.Epoch, Parents: event.Parents, SelectedParent: event.SelectedParent, RecoveryIDs: event.RecoveryIDs, Action: event.Action, DeviceID: event.DeviceID, DeviceName: event.DeviceName, DevicePublicKey: publicKey, Role: event.Role, SignerID: event.SignerID, SignerPublicKey: event.SignerPublicKey}
	body, err := json.Marshal(core)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if event.ID != hex.EncodeToString(digest[:]) {
		return errors.New("network event content ID does not match body")
	}
	if event.Action != "resolve" && event.Action != "recover" && (len(event.DevicePublicKey) != ed25519.PublicKeySize || event.DeviceID != device.IDForPublicKey(event.DevicePublicKey)) {
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

func (store *Store) mutableNetworkBundle(ctx context.Context) (NetworkBundle, error) {
	bundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return NetworkBundle{}, err
	}
	analysis, err := analyzeNetwork(bundle)
	if err != nil {
		return NetworkBundle{}, err
	}
	if analysis.conflict != nil {
		return NetworkBundle{}, ErrNetworkConflict
	}
	return bundle, nil
}

func (store *Store) persistNetworkEvents(ctx context.Context, networkID string, epoch int64, events []NetworkEvent) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertNetworkEvents(ctx, tx, events); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE networks SET epoch=MAX(epoch,?) WHERE id=?`, epoch, networkID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("network changed during membership update")
	}
	bundle, analysis, err := analyzePersistedNetwork(ctx, tx, networkID)
	if err != nil {
		return err
	}
	if err = store.reconcilePersistedNetwork(ctx, tx, bundle, analysis); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if analysis.conflict != nil {
		return ErrNetworkConflict
	}
	return nil
}

func exportNetworkTx(ctx context.Context, tx *sql.Tx, networkID string) (NetworkBundle, error) {
	var bundle NetworkBundle
	if err := tx.QueryRowContext(ctx, `SELECT id,epoch FROM networks WHERE id=?`, networkID).Scan(&bundle.ID, &bundle.Epoch); err != nil {
		return NetworkBundle{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,network_id,epoch,version,parents,selected_parent,recovery_ids,action,device_id,device_name,device_public_key,role,signer_id,signer_public_key,signature FROM network_events WHERE network_id=? ORDER BY epoch,id`, networkID)
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
	return bundle, rows.Err()
}

func requireNetworkEventAdmin(state NetworkState, event NetworkEvent) error {
	signer, found := findDevice(state.Devices, event.SignerID)
	if !found || !signer.Active || signer.Role != NetworkAdmin || string(signer.PublicKey) != string(event.SignerPublicKey) {
		return errors.New("network event signer is not an active admin at its causal parent")
	}
	return nil
}

func networkEventIDs(events []NetworkEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	sort.Strings(ids)
	return ids
}

func networkFrontier(events []NetworkEvent) []string {
	var causal []NetworkEvent
	for _, event := range events {
		if event.Version == 1 {
			causal = append(causal, event)
		}
	}
	if len(causal) == 0 {
		return networkEventIDs(events)
	}
	child := map[string]bool{}
	for _, event := range causal {
		for _, parent := range event.Parents {
			child[parent] = true
		}
	}
	var heads []string
	for _, event := range causal {
		if !child[event.ID] {
			heads = append(heads, event.ID)
		}
	}
	sort.Strings(heads)
	return heads
}

func cloneNetworkState(state NetworkState) NetworkState {
	result := state
	result.Devices = sortedDevices(deviceMap(state.Devices))
	return result
}

func deviceMap(devices []DeviceRecord) map[string]DeviceRecord {
	result := make(map[string]DeviceRecord, len(devices))
	for _, record := range devices {
		record.PublicKey = append([]byte(nil), record.PublicKey...)
		result[record.ID] = record
	}
	return result
}

func sortedDevices(devices map[string]DeviceRecord) []DeviceRecord {
	result := make([]DeviceRecord, 0, len(devices))
	for _, record := range devices {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func containsDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}
