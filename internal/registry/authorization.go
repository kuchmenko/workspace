package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	case "ordinary", "resolution", "access":
		wantParents = 1
	case "merge":
		wantParents = 2
	default:
		return errors.New("workspace revision kind is invalid")
	}
	if len(revision.Parents) != wantParents {
		return errors.New("workspace revision has invalid parent count")
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
	for index, head := range heads {
		revision, found := revisions[head]
		if head == "" || !found || revision.Epoch != bundle.Epoch || index > 0 && heads[index-1] == head {
			return nil, errors.New("bundle contains an invalid head set")
		}
	}
	return heads, nil
}

func (store *Store) validateAuthorization(ctx context.Context, revisions map[string]Revision) error {
	network, err := store.Network(ctx)
	if err != nil {
		return err
	}
	keys := make(map[string]string, len(network.Devices))
	for _, record := range network.Devices {
		keys[record.ID] = string(record.PublicKey)
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
			if err = authorizeRevision(remaining[id], validated, keys); err != nil {
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

func authorizeRevision(revision Revision, validated map[string]Revision, keys map[string]string) error {
	if revision.Access == nil {
		return authorizeLegacyRevision(revision, validated, keys)
	}
	policy, err := normalizePolicy(*revision.Access)
	if err != nil {
		return err
	}
	authorPolicy, err := validateRevisionPolicy(revision, policy, validated)
	if err != nil {
		return err
	}
	return authorizeProofs(revision, authorPolicy, keys)
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
	parent := validated[revision.Parents[0]]
	if parent.Access == nil && revision.Kind != "access" {
		return AccessPolicy{}, errors.New("first policy revision after legacy history must be an access anchor")
	}
	authorPolicy := policy
	if parent.Access != nil {
		authorPolicy = *parent.Access
	}
	if revision.Kind == "access" {
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

func authorizeProofs(revision Revision, policy AccessPolicy, keys map[string]string) error {
	for _, proof := range revision.Proofs {
		if keys[proof.DeviceID] != string(proof.PublicKey) {
			return errors.New("revision author is not a known network device")
		}
		role := policy.Role(proof.DeviceID, true)
		if revision.Kind == "access" && role != WorkspaceAdmin {
			return errors.New("access revision is not signed by a workspace admin")
		}
		if revision.Kind != "access" && role != WorkspaceAdmin && role != WorkspaceWriter {
			return errors.New("revision is not signed by a workspace writer")
		}
	}
	return nil
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
