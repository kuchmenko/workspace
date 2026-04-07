package alias

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
)

// TargetKind tells whether an alias target is a project or a group.
type TargetKind int

const (
	TargetUnknown TargetKind = iota
	TargetProject
	TargetGroup
	TargetRoot
)

// RootTarget is the sentinel target value that resolves to the workspace root.
const RootTarget = "."

func (k TargetKind) String() string {
	switch k {
	case TargetProject:
		return "project"
	case TargetGroup:
		return "group"
	case TargetRoot:
		return "root"
	}
	return "unknown"
}

// Resolved is a fully-resolved alias entry.
type Resolved struct {
	Name   string     // alias name (key)
	Target string     // raw target (project or group key)
	Kind   TargetKind
	Path   string     // absolute filesystem path
}

// Resolve looks up a single alias and returns its absolute path.
func Resolve(ws *config.Workspace, root, name string) (Resolved, error) {
	target, ok := ws.Aliases[name]
	if !ok {
		return Resolved{}, fmt.Errorf("alias %q not defined", name)
	}
	return resolveTarget(ws, root, name, target)
}

// ResolveAll returns every alias resolved, sorted by alias name.
// Aliases that fail to resolve are returned with Kind=TargetUnknown
// and an empty Path so callers can flag them.
func ResolveAll(ws *config.Workspace, root string) []Resolved {
	out := make([]Resolved, 0, len(ws.Aliases))
	for name, target := range ws.Aliases {
		r, err := resolveTarget(ws, root, name, target)
		if err != nil {
			r = Resolved{Name: name, Target: target, Kind: TargetUnknown}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func resolveTarget(ws *config.Workspace, root, name, target string) (Resolved, error) {
	if target == RootTarget {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetRoot,
			Path:   root,
		}, nil
	}
	if proj, ok := ws.Projects[target]; ok {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetProject,
			Path:   filepath.Join(root, proj.Path),
		}, nil
	}
	if _, ok := ws.Groups[target]; ok {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetGroup,
			Path:   filepath.Join(root, target),
		}, nil
	}
	return Resolved{}, fmt.Errorf("alias %q points to unknown target %q", name, target)
}

// RemoveForTarget deletes every alias whose target equals `target`.
// Returns the names removed.
func RemoveForTarget(ws *config.Workspace, target string) []string {
	var removed []string
	for name, t := range ws.Aliases {
		if t == target {
			removed = append(removed, name)
			delete(ws.Aliases, name)
		}
	}
	sort.Strings(removed)
	return removed
}
