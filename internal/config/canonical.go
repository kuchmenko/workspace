package config

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

func DecodeWorkspace(data []byte) (*Workspace, error) {
	return decodeWorkspace(data, false)
}

func DecodeWorkspaceForImport(data []byte) (*Workspace, error) {
	return decodeWorkspace(data, true)
}

func decodeWorkspace(data []byte, mergeBranches bool) (*Workspace, error) {
	var ws Workspace
	metadata, err := toml.Decode(string(data), &ws)
	if err != nil {
		return nil, fmt.Errorf("decode workspace: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return nil, fmt.Errorf("decode workspace: unknown fields: %v", undecoded)
	}
	if ws.Meta.Version != 1 {
		return nil, fmt.Errorf("decode workspace: unsupported schema version %d", ws.Meta.Version)
	}
	normalizeWorkspace(&ws)
	if mergeBranches {
		for _, issue := range ws.Validate() {
			if issue.Kind != ValidationDuplicateBranch {
				return nil, fmt.Errorf("invalid workspace: %v", issue)
			}
		}
		mergeDuplicateBranches(&ws)
	}
	if issues := ws.Validate(); len(issues) != 0 {
		return nil, fmt.Errorf("invalid workspace: %v", issues)
	}
	return &ws, nil
}

func mergeDuplicateBranches(workspace *Workspace) {
	for name, project := range workspace.Projects {
		branches := make(map[string]BranchMeta, len(project.Branches))
		for _, branch := range project.Branches {
			existing, found := branches[branch.Name]
			if found {
				branch = mergeBranch(existing, branch)
			}
			branches[branch.Name] = branch
		}
		project.Branches = project.Branches[:0]
		for _, branch := range branches {
			project.Branches = append(project.Branches, branch)
		}
		sort.Slice(project.Branches, func(left, right int) bool {
			return project.Branches[left].Name < project.Branches[right].Name
		})
		workspace.Projects[name] = project
	}
}

func mergeBranch(left, right BranchMeta) BranchMeta {
	left.Machines = sortedDedup(append(left.Machines, right.Machines...))
	left.LastActiveMachine, left.LastActiveAt = latestObservation(left.LastActiveMachine, left.LastActiveAt, right.LastActiveMachine, right.LastActiveAt)
	left.LastPushedMachine, left.LastPushedAt = latestObservation(left.LastPushedMachine, left.LastPushedAt, right.LastPushedMachine, right.LastPushedAt)
	left.CreatedBy, left.CreatedAt = earliestObservation(left.CreatedBy, left.CreatedAt, right.CreatedBy, right.CreatedAt)
	return left
}

func latestObservation(leftMachine, leftAt, rightMachine, rightAt string) (string, string) {
	if compareObservation(leftMachine, leftAt, rightMachine, rightAt) < 0 {
		return rightMachine, rightAt
	}
	return leftMachine, leftAt
}

func earliestObservation(leftMachine, leftAt, rightMachine, rightAt string) (string, string) {
	if leftAt == "" || rightAt != "" && compareObservation(leftMachine, leftAt, rightMachine, rightAt) > 0 {
		return rightMachine, rightAt
	}
	return leftMachine, leftAt
}

func compareObservation(leftMachine, leftAt, rightMachine, rightAt string) int {
	leftTime, leftErr := time.Parse(time.RFC3339, leftAt)
	rightTime, rightErr := time.Parse(time.RFC3339, rightAt)
	if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
		if leftTime.Before(rightTime) {
			return -1
		}
		return 1
	}
	if leftAt != rightAt {
		return bytes.Compare([]byte(leftAt), []byte(rightAt))
	}
	return bytes.Compare([]byte(leftMachine), []byte(rightMachine))
}

func EncodeCanonicalWorkspace(ws *Workspace) ([]byte, error) {
	cloned := cloneWorkspace(ws)
	cloned.Meta.Root = ""
	if cloned.Meta.Version != 1 {
		return nil, fmt.Errorf("encode workspace: unsupported schema version %d", cloned.Meta.Version)
	}
	normalizeWorkspace(cloned)
	if issues := cloned.Validate(); len(issues) != 0 {
		return nil, fmt.Errorf("invalid workspace: %v", issues)
	}
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(cloned); err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}
	return out.Bytes(), nil
}

func cloneWorkspace(ws *Workspace) *Workspace {
	out := *ws
	out.Groups = make(map[string]Group, len(ws.Groups))
	for key, value := range ws.Groups {
		out.Groups[key] = value
	}
	out.Aliases = make(map[string]string, len(ws.Aliases))
	for key, value := range ws.Aliases {
		out.Aliases[key] = value
	}
	out.Projects = make(map[string]Project, len(ws.Projects))
	for key, value := range ws.Projects {
		value.Mirrors = cloneStringMap(value.Mirrors)
		value.Branches = append([]BranchMeta(nil), value.Branches...)
		for i := range value.Branches {
			value.Branches[i].Machines = append([]string(nil), value.Branches[i].Machines...)
		}
		out.Projects[key] = value
	}
	return &out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func DecodeCanonicalWorkspace(data []byte) (*Workspace, error) {
	ws, err := DecodeWorkspace(data)
	if err != nil {
		return nil, err
	}
	ws.Meta.Root = ""
	return ws, nil
}

func (w *Workspace) RestoreRoot(root string) {
	w.Meta.Root = root
}

func normalizeWorkspace(ws *Workspace) {
	if ws.Groups == nil {
		ws.Groups = map[string]Group{}
	}
	if ws.Projects == nil {
		ws.Projects = map[string]Project{}
	}
	if ws.Aliases == nil {
		ws.Aliases = map[string]string{}
	}
	for name, project := range ws.Projects {
		if project.Mirrors == nil {
			project.Mirrors = map[string]string{}
		}
		for i := range project.Branches {
			project.Branches[i].Machines = sortedDedup(project.Branches[i].Machines)
		}
		sort.Slice(project.Branches, func(i, j int) bool { return project.Branches[i].Name < project.Branches[j].Name })
		project.LegacyAutopush = nil
		ws.Projects[name] = project
	}
}
