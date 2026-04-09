package agent

import (
	"fmt"
	"sort"
)

// Template builds a synthetic graph for visual validation. Templates are
// deterministic, do not touch the filesystem, and exist so the user and
// the assistant can sync visual expectations against canonical fixtures
// without having to set up a real workspace.
//
// Use via `ws agent --template <name>`.
type Template struct {
	Name        string
	Description string
	Build       func() *Graph
}

var templateRegistry = map[string]Template{
	"tiny": {
		Name:        "tiny",
		Description: "1 ws + 2 projects, no worktrees — minimal sanity check",
		Build:       buildTiny,
	},
	"small": {
		Name:        "small",
		Description: "1 ws + 5 projects + actions",
		Build:       buildSmall,
	},
	"wide": {
		Name:        "wide",
		Description: "1 ws + 30 projects — stress 'more' portal / overflow",
		Build:       buildWide,
	},
	"deep": {
		Name:        "deep",
		Description: "1 ws + 1 project + 5 worktrees + actions per worktree",
		Build:       buildDeep,
	},
	"realistic": {
		Name:        "realistic",
		Description: "3 ws with groups, varied depth, realistic names",
		Build:       buildRealistic,
	},
	"pathological": {
		Name:        "pathological",
		Description: "1 ws with 100 projects + 1 deep — extreme overflow",
		Build:       buildPathological,
	},
}

// LoadTemplate returns a graph for the named template, or an error if
// the name is unknown. The cross renderer computes positions per render,
// so no layout pass is needed here.
func LoadTemplate(name string) (*Graph, error) {
	t, ok := templateRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown template %q (try: %s)", name, knownTemplateNames())
	}
	return t.Build(), nil
}

// TemplateNames returns the registered template names in alphabetical
// order, suitable for `--help` listings.
func TemplateNames() []string { return knownTemplateNamesList() }

func knownTemplateNamesList() []string {
	out := make([]string, 0, len(templateRegistry))
	for n := range templateRegistry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func knownTemplateNames() string {
	names := knownTemplateNamesList()
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

// ---------------- builders ----------------

// Single-workspace templates use the workspace itself as the root,
// matching the BuildGraph behavior when only one ws is registered.

func buildTiny() *Graph {
	g := NewGraph(&Node{ID: "ws/dev", Kind: KindWorkspace, Label: "dev"})
	g.Add("ws/dev", &Node{ID: "ws/dev/foo", Kind: KindProject, Label: "foo"})
	g.Add("ws/dev", &Node{ID: "ws/dev/bar", Kind: KindProject, Label: "bar"})
	return g
}

func buildSmall() *Graph {
	g := NewGraph(&Node{ID: "ws/dev", Kind: KindWorkspace, Label: "dev"})
	for _, p := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		g.Add("ws/dev", &Node{ID: "ws/dev/" + p, Kind: KindProject, Label: p})
	}
	return g
}

func buildWide() *Graph {
	g := NewGraph(&Node{ID: "ws/dev", Kind: KindWorkspace, Label: "dev"})
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("proj-%02d", i)
		g.Add("ws/dev", &Node{ID: "ws/dev/" + name, Kind: KindProject, Label: name})
	}
	return g
}

func buildDeep() *Graph {
	g := NewGraph(&Node{ID: "ws/dev", Kind: KindWorkspace, Label: "dev"})
	// Deep nesting: workspace → group → project chain, 8 levels.
	g.Add("ws/dev", &Node{ID: "ws/dev/tools", Kind: KindGroup, Label: "tools"})
	g.Add("ws/dev/tools", &Node{ID: "ws/dev/myapp", Kind: KindProject, Label: "myapp"})
	g.Add("ws/dev/tools", &Node{ID: "ws/dev/cli", Kind: KindProject, Label: "cli"})
	g.Add("ws/dev/tools", &Node{ID: "ws/dev/sdk", Kind: KindProject, Label: "sdk"})
	g.Add("ws/dev", &Node{ID: "ws/dev/web", Kind: KindGroup, Label: "web"})
	g.Add("ws/dev/web", &Node{ID: "ws/dev/blog", Kind: KindProject, Label: "blog"})
	g.Add("ws/dev/web", &Node{ID: "ws/dev/docs", Kind: KindProject, Label: "docs"})
	return g
}

func buildRealistic() *Graph {
	g := NewGraph(&Node{ID: "ws", Kind: KindRoot, Label: "ws"})

	// Workspace 1: with groups
	g.Add("ws", &Node{ID: "ws/personal", Kind: KindWorkspace, Label: "personal"})
	g.Add("ws/personal", &Node{ID: "ws/personal/g:tools", Kind: KindGroup, Label: "tools"})
	g.Add("ws/personal", &Node{ID: "ws/personal/g:web", Kind: KindGroup, Label: "web"})
	for _, p := range []string{"workspace", "pulse", "ws"} {
		g.Add("ws/personal/g:tools", &Node{ID: "ws/personal/" + p, Kind: KindProject, Label: p})
	}
	for _, p := range []string{"blog", "portfolio"} {
		g.Add("ws/personal/g:web", &Node{ID: "ws/personal/" + p, Kind: KindProject, Label: p})
	}
	g.Add("ws/personal", &Node{ID: "ws/personal/dotfiles", Kind: KindProject, Label: "dotfiles"})

	// Workspace 2: flat
	g.Add("ws", &Node{ID: "ws/work", Kind: KindWorkspace, Label: "work"})
	for _, p := range []string{"api", "dashboard", "ml", "infra"} {
		g.Add("ws/work", &Node{ID: "ws/work/" + p, Kind: KindProject, Label: p})
	}

	// Workspace 3: tiny
	g.Add("ws", &Node{ID: "ws/playground", Kind: KindWorkspace, Label: "playground"})
	g.Add("ws/playground", &Node{ID: "ws/playground/scratch", Kind: KindProject, Label: "scratch"})

	return g
}

func buildPathological() *Graph {
	g := NewGraph(&Node{ID: "ws", Kind: KindRoot, Label: "ws"})
	g.Add("ws", &Node{ID: "ws/big", Kind: KindWorkspace, Label: "big"})
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("p%03d", i)
		g.Add("ws/big", &Node{ID: "ws/big/" + name, Kind: KindProject, Label: name})
	}
	g.Add("ws", &Node{ID: "ws/deep", Kind: KindWorkspace, Label: "deep"})
	parent := "ws/deep"
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("ws/deep/lvl%d", i)
		g.Add(parent, &Node{ID: id, Kind: KindProject, Label: fmt.Sprintf("lvl%d", i)})
		parent = id
	}
	return g
}
