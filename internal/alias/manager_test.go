package alias

import (
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestBuildItemsChoosesAliasForDuplicateTargetDeterministically(t *testing.T) {
	ws := &config.Workspace{
		Projects: map[string]config.Project{"project": {}},
		Groups:   map[string]config.Group{},
		Aliases:  map[string]string{"z": "project", "a": "project"},
	}

	for i := 0; i < 20; i++ {
		items := buildItems(ws)
		var projectAlias string
		for _, item := range items {
			if item.kind == kindProject && item.name == "project" {
				projectAlias = item.alias
			}
		}
		if projectAlias != "a" {
			t.Fatalf("buildItems() selected %q, want a", projectAlias)
		}
	}
}

func TestBuildAliasMapHasOneAliasPerTarget(t *testing.T) {
	m := ManagerModel{items: []item{
		{name: "one", kind: kindProject, alias: "first", checked: true},
		{name: "two", kind: kindProject, alias: "second", checked: true},
	}}

	aliases := m.buildAliasMap()
	if len(aliases) != 2 || aliases["first"] != "one" || aliases["second"] != "two" {
		t.Fatalf("buildAliasMap() = %#v", aliases)
	}
}
