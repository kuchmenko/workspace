package alias

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
)

type TargetKind int

const (
	TargetUnknown TargetKind = iota
	TargetProject
	TargetGroup
	TargetRoot
)

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

type Resolved struct {
	Name   string
	Target string
	Kind   TargetKind
	Path   string
}

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
