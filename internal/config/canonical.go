package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"
)

func DecodeWorkspace(data []byte) (*Workspace, error) {
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
	if issues := ws.Validate(); len(issues) != 0 {
		return nil, fmt.Errorf("invalid workspace: %v", issues)
	}
	return &ws, nil
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
