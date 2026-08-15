package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

type Conflict struct {
	Path  string          `json:"path"`
	Base  json.RawMessage `json:"base,omitempty"`
	Left  json.RawMessage `json:"left,omitempty"`
	Right json.RawMessage `json:"right,omitempty"`
}

func mergeSnapshots(baseBody, leftBody, rightBody []byte) ([]byte, []Conflict, error) {
	var base, left, right any
	for body, target := range map[string]*any{"base": &base, "left": &left, "right": &right} {
		var source []byte
		switch body {
		case "base":
			source = baseBody
		case "left":
			source = leftBody
		default:
			source = rightBody
		}
		if err := json.Unmarshal(source, target); err != nil {
			return nil, nil, fmt.Errorf("decode %s snapshot: %w", body, err)
		}
	}
	var conflicts []Conflict
	merged, _, err := mergeValue("", base, true, left, true, right, true, &conflicts)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(merged)
	if err != nil {
		return nil, nil, err
	}
	if _, err = decodeSnapshot(body); err != nil {
		return nil, nil, fmt.Errorf("validate merged snapshot: %w", err)
	}
	return body, conflicts, nil
}

func Merge(base, left, right *config.Workspace) (*config.Workspace, []Conflict, error) {
	baseBody, err := encodeSnapshot(base)
	if err != nil {
		return nil, nil, err
	}
	leftBody, err := encodeSnapshot(left)
	if err != nil {
		return nil, nil, err
	}
	rightBody, err := encodeSnapshot(right)
	if err != nil {
		return nil, nil, err
	}
	mergedBody, conflicts, err := mergeSnapshots(baseBody, leftBody, rightBody)
	if err != nil {
		return nil, nil, err
	}
	merged, err := decodeSnapshot(mergedBody)
	return merged, conflicts, err
}

func mergeValue(path string, base any, hasBase bool, left any, hasLeft bool, right any, hasRight bool, conflicts *[]Conflict) (any, bool, error) {
	if hasLeft && hasRight && reflect.DeepEqual(left, right) {
		return left, true, nil
	}
	if hasBase && hasLeft && reflect.DeepEqual(base, left) {
		return right, hasRight, nil
	}
	if hasBase && hasRight && reflect.DeepEqual(base, right) {
		return left, hasLeft, nil
	}
	if hasBase && !hasLeft && !hasRight {
		return nil, false, nil
	}
	if hasBase && !hasLeft {
		if reflect.DeepEqual(base, right) {
			return nil, false, nil
		}
		appendConflict(path, base, nil, right, conflicts)
		return base, true, nil
	}
	if hasBase && !hasRight {
		if reflect.DeepEqual(base, left) {
			return nil, false, nil
		}
		appendConflict(path, base, left, nil, conflicts)
		return base, true, nil
	}
	if !hasBase && !hasLeft {
		return right, hasRight, nil
	}
	if !hasBase && !hasRight {
		return left, hasLeft, nil
	}

	baseMap, baseIsMap := base.(map[string]any)
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap && isObservationPath(path) {
		return mergeObservation(path, leftMap, rightMap), true, nil
	}
	if leftIsMap && rightIsMap && (!hasBase || baseIsMap) {
		keys := make(map[string]bool, len(baseMap)+len(leftMap)+len(rightMap))
		for key := range baseMap {
			keys[key] = true
		}
		for key := range leftMap {
			keys[key] = true
		}
		for key := range rightMap {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		result := make(map[string]any, len(ordered))
		for _, key := range ordered {
			baseValue, basePresent := baseMap[key]
			leftValue, leftPresent := leftMap[key]
			rightValue, rightPresent := rightMap[key]
			value, present, err := mergeValue(joinPath(path, key), baseValue, basePresent, leftValue, leftPresent, rightValue, rightPresent, conflicts)
			if err != nil {
				return nil, false, err
			}
			if present {
				result[key] = value
			}
		}
		return result, true, nil
	}

	appendConflict(path, optionalValue(base, hasBase), optionalValue(left, hasLeft), optionalValue(right, hasRight), conflicts)
	if hasBase {
		return base, true, nil
	}
	leftBody, err := json.Marshal(left)
	if err != nil {
		return nil, false, err
	}
	rightBody, err := json.Marshal(right)
	if err != nil {
		return nil, false, err
	}
	if bytes.Compare(leftBody, rightBody) <= 0 {
		return left, true, nil
	}
	return right, true, nil
}

func optionalValue(value any, present bool) any {
	if !present {
		return nil
	}
	return value
}

func appendConflict(path string, base, left, right any, conflicts *[]Conflict) {
	*conflicts = append(*conflicts, Conflict{
		Path:  path,
		Base:  marshalValue(base),
		Left:  marshalValue(left),
		Right: marshalValue(right),
	})
}

func marshalValue(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	body, _ := json.Marshal(value)
	return body
}

func joinPath(parent, key string) string {
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	return parent + "/" + key
}

func isObservationPath(path string) bool {
	return strings.HasSuffix(path, "/last_active") || strings.HasSuffix(path, "/last_pushed") || strings.HasSuffix(path, "/created")
}

func mergeObservation(path string, left, right map[string]any) map[string]any {
	leftAt, _ := left["at"].(string)
	rightAt, _ := right["at"].(string)
	leftMachine, _ := left["machine"].(string)
	rightMachine, _ := right["machine"].(string)
	comparison := compareObservation(leftAt, leftMachine, rightAt, rightMachine)
	if strings.HasSuffix(path, "/created") {
		if leftAt == "" || rightAt != "" && comparison > 0 {
			return right
		}
		return left
	}
	if comparison < 0 {
		return right
	}
	return left
}

func compareObservation(leftAt, leftMachine, rightAt, rightMachine string) int {
	leftTime, leftErr := time.Parse(time.RFC3339Nano, leftAt)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, rightAt)
	if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
		if leftTime.Before(rightTime) {
			return -1
		}
		return 1
	}
	if leftAt != rightAt {
		return strings.Compare(leftAt, rightAt)
	}
	return strings.Compare(leftMachine, rightMachine)
}
