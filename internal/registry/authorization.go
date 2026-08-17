package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	maxBundleRevisions = 10000
	maxBundleHeads     = 64
	maxRevisionBytes   = 8 << 20
	maxRevisionItems   = 1024
)

func (store *Store) validateBundle(ctx context.Context, bundle Bundle, authorize bool) ([]string, error) {
	revisions, err := validateBundleStructure(bundle)
	if err != nil {
		return nil, err
	}
	heads, err := validateBundleHeads(bundle, revisions)
	if err != nil {
		return nil, err
	}
	if authorize {
		if err = store.validateAuthorization(ctx, revisions); err != nil {
			return nil, err
		}
	}
	return heads, nil
}

func validateBundleStructure(bundle Bundle) (map[string]Revision, error) {
	if bundle.WorkspaceID == "" || bundle.Epoch < 1 {
		return nil, errors.New("bundle workspace identity is invalid")
	}
	if len(bundle.Heads) == 0 {
		return nil, errors.New("bundle must contain at least one head")
	}
	if len(bundle.Revisions) > maxBundleRevisions || len(bundle.Heads) > maxBundleHeads {
		return nil, errors.New("bundle history limit exceeded")
	}
	revisions := make(map[string]Revision, len(bundle.Revisions))
	for _, revision := range bundle.Revisions {
		if err := validateBundledRevision(bundle, revision, revisions); err != nil {
			return nil, err
		}
		revisions[revision.ID] = revision
	}
	if err := validateCompleteHistory(bundle.Revisions, revisions); err != nil {
		return nil, err
	}
	if err := validateGenesis(revisions); err != nil {
		return nil, err
	}
	if err := validatePolicyAnchor(revisions); err != nil {
		return nil, err
	}
	return revisions, nil
}

func validateBundledRevision(bundle Bundle, revision Revision, revisions map[string]Revision) error {
	if revision.WorkspaceID != bundle.WorkspaceID || revision.Epoch < 1 || revision.Epoch > bundle.Epoch {
		return errors.New("bundle contains a revision for another workspace or future epoch")
	}
	if _, exists := revisions[revision.ID]; exists {
		return errors.New("bundle contains a duplicate revision")
	}
	if len(revision.Snapshot) > maxRevisionBytes || len(revision.Conflicts) > maxRevisionItems || len(revision.Proofs) > maxRevisionItems {
		return errors.New("workspace revision size limit exceeded")
	}
	if err := validateRevisionShape(revision); err != nil {
		return err
	}
	return verifyRevision(revision)
}

func validateCompleteHistory(revisions []Revision, indexed map[string]Revision) error {
	for _, revision := range revisions {
		for _, parent := range revision.Parents {
			if _, found := indexed[parent]; !found {
				return errors.New("bundle revision history is incomplete")
			}
		}
	}
	return nil
}

func validateRevisionShape(revision Revision) error {
	var wantParents int
	switch revision.Kind {
	case "genesis":
		wantParents = 0
		if revision.Epoch != 1 {
			return errors.New("workspace genesis must use epoch 1")
		}
	case "ordinary", "resolution", "access", "access-recovery":
		wantParents = 1
	case "merge":
		wantParents = 2
	case "access-resolution":
		if len(revision.Parents) < 2 || len(revision.Parents) > maxBundleHeads {
			return errors.New("access resolution has invalid parent count")
		}
		wantParents = len(revision.Parents)
	default:
		return errors.New("workspace revision kind is invalid")
	}
	if len(revision.Parents) != wantParents {
		return errors.New("workspace revision has invalid parent count")
	}
	if revision.Kind == "access-recovery" && revision.NetworkHead == "" {
		return errors.New("workspace admin recovery has no network authority head")
	}
	if revision.Kind != "access-recovery" && revision.NetworkHead != "" {
		return errors.New("workspace revision has unexpected network authority metadata")
	}
	for index := 1; index < len(revision.Parents); index++ {
		if revision.Parents[index-1] == revision.Parents[index] {
			return errors.New("workspace revision contains a duplicate parent")
		}
	}
	return nil
}

func validateGenesis(revisions map[string]Revision) error {
	genesis := 0
	for _, revision := range revisions {
		if revision.Kind == "genesis" {
			genesis++
		}
	}
	if genesis != 1 {
		return errors.New("bundle must contain exactly one genesis revision")
	}
	return nil
}

func validatePolicyAnchor(revisions map[string]Revision) error {
	anchors := 0
	for _, revision := range revisions {
		if revision.Access == nil {
			continue
		}
		if len(revision.Parents) == 0 || revisions[revision.Parents[0]].Access == nil {
			anchors++
		}
	}
	if anchors != 1 {
		return errors.New("bundle must contain exactly one workspace policy anchor")
	}
	return nil
}

func validateBundleHeads(bundle Bundle, revisions map[string]Revision) ([]string, error) {
	heads := append([]string(nil), bundle.Heads...)
	sort.Strings(heads)
	highest := false
	for index, head := range heads {
		revision, found := revisions[head]
		if head == "" || !found || revision.Epoch > bundle.Epoch || index > 0 && heads[index-1] == head {
			return nil, errors.New("bundle contains an invalid head set")
		}
		highest = highest || revision.Epoch == bundle.Epoch
	}
	if !highest {
		return nil, errors.New("bundle epoch has no matching head")
	}
	if len(heads) > 1 {
		policy := revisionPolicyMap(revisions, heads[0])
		mixedEpoch := false
		for _, head := range heads[1:] {
			mixedEpoch = mixedEpoch || revisions[head].Epoch != revisions[heads[0]].Epoch
			if mixedEpoch && equalPolicy(policy, revisionPolicyMap(revisions, head)) {
				return nil, errors.New("mixed-epoch heads require divergent access policies")
			}
		}
	}
	return heads, nil
}

func revisionPolicyMap(revisions map[string]Revision, id string) AccessPolicy {
	if revision := revisions[id]; revision.Access != nil {
		return *revision.Access
	}
	return AccessPolicy{}
}

func (store *Store) validateAuthorization(ctx context.Context, revisions map[string]Revision) error {
	network, err := store.Network(ctx)
	if err != nil {
		return err
	}
	devices := make(map[string]DeviceRecord, len(network.Devices))
	for _, record := range network.Devices {
		devices[record.ID] = record
	}
	networkBundle, err := store.ExportNetwork(ctx)
	if err != nil {
		return err
	}
	remaining := make(map[string]Revision, len(revisions))
	for id, revision := range revisions {
		remaining[id] = revision
	}
	validated := map[string]Revision{}
	for len(remaining) > 0 {
		ready := readyRevisions(remaining, validated)
		if len(ready) == 0 {
			return errors.New("workspace revision graph contains a cycle")
		}
		for _, id := range ready {
			if err = authorizeRevision(remaining[id], validated, devices, networkBundle); err != nil {
				return fmt.Errorf("authorize revision %s: %w", id, err)
			}
			validated[id] = remaining[id]
			delete(remaining, id)
		}
	}
	return nil
}

func readyRevisions(remaining, validated map[string]Revision) []string {
	var ready []string
	for id, revision := range remaining {
		allParentsReady := true
		for _, parent := range revision.Parents {
			if _, found := validated[parent]; !found {
				allParentsReady = false
				break
			}
		}
		if allParentsReady {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	return ready
}

func authorizeRevision(revision Revision, validated map[string]Revision, devices map[string]DeviceRecord, network NetworkBundle) error {
	if revision.Access == nil {
		return authorizeLegacyRevision(revision, validated, deviceKeys(devices))
	}
	policy, err := normalizePolicy(*revision.Access)
	if err != nil {
		return err
	}
	authorPolicy, err := validateRevisionPolicy(revision, policy, validated)
	if err != nil {
		return err
	}
	return authorizeProofs(revision, authorPolicy, devices, network)
}

func authorizeLegacyRevision(revision Revision, validated map[string]Revision, keys map[string]string) error {
	if len(revision.Parents) != 0 && !allParentsLegacy(revision, validated) {
		return errors.New("policy-less revision follows a policy anchor")
	}
	for _, proof := range revision.Proofs {
		if keys[proof.DeviceID] != string(proof.PublicKey) {
			return errors.New("legacy revision author is not a known network device")
		}
	}
	return nil
}

func validateRevisionPolicy(revision Revision, policy AccessPolicy, validated map[string]Revision) (AccessPolicy, error) {
	if len(revision.Parents) == 0 {
		return policy, nil
	}
	if len(revision.Parents) > 1 {
		baseID, found := commonValidatedAncestor(revision.Parents, validated)
		if !found || !equalValidatedRevisionKindSets(revision.Parents, baseID, "access-resolution", validated) {
			return AccessPolicy{}, errors.New("workspace revision cannot graft a stale policy branch")
		}
	}
	if revision.Kind == "access-resolution" {
		return validateAccessResolution(revision, policy, validated)
	}
	parent := validated[revision.Parents[0]]
	if parent.Access == nil && revision.Kind != "access" {
		return AccessPolicy{}, errors.New("first policy revision after legacy history must be an access anchor")
	}
	authorPolicy := policy
	if parent.Access != nil {
		authorPolicy = *parent.Access
	}
	if revision.Kind == "access" || revision.Kind == "access-recovery" {
		return authorPolicy, validateAccessTransition(parent, revision, policy)
	}
	for _, parentID := range revision.Parents {
		parentRevision := validated[parentID]
		if parentRevision.Access == nil || !equalPolicy(policy, *parentRevision.Access) || parentRevision.Epoch != revision.Epoch {
			return AccessPolicy{}, errors.New("ordinary revision changes access policy or epoch")
		}
	}
	return authorPolicy, nil
}

func validateAccessResolution(revision Revision, policy AccessPolicy, validated map[string]Revision) (AccessPolicy, error) {
	if len(revision.Parents) < 2 {
		return AccessPolicy{}, errors.New("access resolution requires multiple parents")
	}
	baseID, found := commonValidatedAncestor(revision.Parents, validated)
	if !found || validated[baseID].Access == nil {
		return AccessPolicy{}, errors.New("access resolution has no policy base")
	}
	policySelected := false
	stateSelected := false
	maxEpoch := int64(0)
	conflicts, _ := json.Marshal(revision.Conflicts)
	for _, parentID := range revision.Parents {
		parent := validated[parentID]
		maxEpoch = max(maxEpoch, parent.Epoch)
		policySelected = policySelected || parent.Access != nil && equalPolicy(policy, *parent.Access)
		parentConflicts, _ := json.Marshal(parent.Conflicts)
		stateSelected = stateSelected || string(revision.Snapshot) == string(parent.Snapshot) && string(conflicts) == string(parentConflicts)
	}
	if !policySelected || !stateSelected || revision.Epoch != maxEpoch+1 {
		return AccessPolicy{}, errors.New("access resolution does not select a parent policy and state or has an invalid epoch")
	}
	return *validated[baseID].Access, nil
}

func commonValidatedAncestor(heads []string, revisions map[string]Revision) (string, bool) {
	distances := validatedAncestorDistances(heads[0], revisions)
	best := ""
	bestDistance := int(^uint(0) >> 1)
	for candidate, distance := range distances {
		maximum := distance
		common := true
		for _, head := range heads[1:] {
			other, found := validatedAncestorDistances(head, revisions)[candidate]
			if !found {
				common = false
				break
			}
			maximum = max(maximum, other)
		}
		if common && (maximum < bestDistance || maximum == bestDistance && candidate < best) {
			best, bestDistance = candidate, maximum
		}
	}
	return best, best != ""
}

func validatedAncestorDistances(head string, revisions map[string]Revision) map[string]int {
	distances := map[string]int{head: 0}
	queue := []string{head}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, parent := range revisions[current].Parents {
			if _, found := distances[parent]; found {
				continue
			}
			distances[parent] = distances[current] + 1
			queue = append(queue, parent)
		}
	}
	return distances
}

func validatedRevisionKindIDsBetween(head, base, kind string, revisions map[string]Revision) map[string]bool {
	queue := []string{head}
	seen := map[string]bool{}
	result := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == base || seen[id] {
			continue
		}
		seen[id] = true
		revision := revisions[id]
		if revision.Kind == kind {
			result[id] = true
		}
		queue = append(queue, revision.Parents...)
	}
	return result
}

func equalValidatedRevisionKindSets(heads []string, base, kind string, revisions map[string]Revision) bool {
	if len(heads) < 2 {
		return true
	}
	expected := validatedRevisionKindIDsBetween(heads[0], base, kind, revisions)
	for _, head := range heads[1:] {
		actual := validatedRevisionKindIDsBetween(head, base, kind, revisions)
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

func validateAccessTransition(parent, revision Revision, policy AccessPolicy) error {
	if parent.Access == nil {
		return nil
	}
	expectedEpoch := parent.Epoch
	if policyRestricts(*parent.Access, policy) {
		expectedEpoch++
	}
	if revision.Epoch != expectedEpoch || string(revision.Snapshot) != string(parent.Snapshot) {
		return errors.New("access revision has an invalid epoch or changes workspace state")
	}
	return nil
}

func authorizeProofs(revision Revision, policy AccessPolicy, devices map[string]DeviceRecord, network NetworkBundle) error {
	for _, proof := range revision.Proofs {
		record, known := devices[proof.DeviceID]
		if !known || string(record.PublicKey) != string(proof.PublicKey) {
			return errors.New("revision author is not a known network device")
		}
		if revision.Kind == "access-recovery" {
			if recoveryAuthorizedByNetwork(network, revision.ID, revision.NetworkHead, proof.DeviceID, policy) {
				continue
			}
			return errors.New("workspace admin recovery is not signed by a known network admin")
		}
		role := policy.Role(proof.DeviceID, true)
		accessChange := revision.Kind == "access" || revision.Kind == "access-resolution"
		if accessChange && role != WorkspaceAdmin {
			return errors.New("access revision is not signed by a workspace admin")
		}
		if !accessChange && role != WorkspaceAdmin && role != WorkspaceWriter {
			return errors.New("revision is not signed by a workspace writer")
		}
	}
	return nil
}

func recoveryAuthorizedByNetwork(bundle NetworkBundle, revisionID, authorityHead, deviceID string, policy AccessPolicy) bool {
	current, err := currentCausalNetworkHead(bundle)
	if err != nil || !networkHeadSelectedBy(bundle, current, authorityHead) {
		return false
	}
	ratified := false
	for _, event := range bundle.Events {
		if event.Action == "recover" && len(event.Parents) == 1 && event.Parents[0] == authorityHead && event.SignerID == deviceID && containsString(event.RecoveryIDs, revisionID) && networkHeadSelectedBy(bundle, current, event.ID) {
			ratified = true
			break
		}
	}
	if !ratified {
		return false
	}
	historical, err := networkBundleAtHead(bundle, authorityHead)
	if err != nil {
		return false
	}
	analysis, err := analyzeNetwork(historical)
	if err != nil || analysis.conflict != nil {
		return false
	}
	record, found := findDevice(analysis.state.Devices, deviceID)
	if !found || !record.Active || record.Role != NetworkAdmin {
		return false
	}
	historicalDevices := make(map[string]DeviceRecord, len(analysis.state.Devices))
	for _, historical := range analysis.state.Devices {
		historicalDevices[historical.ID] = historical
	}
	return !policyHasActiveAdmin(policy, historicalDevices)
}

func deviceKeys(devices map[string]DeviceRecord) map[string]string {
	keys := make(map[string]string, len(devices))
	for id, record := range devices {
		keys[id] = string(record.PublicKey)
	}
	return keys
}

func policyHasActiveAdmin(policy AccessPolicy, devices map[string]DeviceRecord) bool {
	for id, role := range policy.Roles {
		if record := devices[id]; role == WorkspaceAdmin && record.Active {
			return true
		}
	}
	return false
}

func allParentsLegacy(revision Revision, validated map[string]Revision) bool {
	for _, parent := range revision.Parents {
		if validated[parent].Access != nil {
			return false
		}
	}
	return true
}

func revisionPolicy(revisions []Revision, id string) AccessPolicy {
	for _, revision := range revisions {
		if revision.ID == id && revision.Access != nil {
			return *revision.Access
		}
	}
	return AccessPolicy{}
}
