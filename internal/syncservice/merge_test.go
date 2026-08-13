package syncservice

import (
	"reflect"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestMergeConflictPayloadUsesBaseClientServiceOrientation(t *testing.T) {
	base := workspace("base")
	client := workspace("client")
	service := workspace("service")
	_, conflicts, err := Merge(base, client, service)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
	conflict := conflicts[0]
	if conflict.Path != "projects.app.remote" || string(conflict.Base) != `"base"` || string(conflict.Local) != `"client"` || string(conflict.Remote) != `"service"` {
		t.Fatalf("conflict orientation: %+v", conflict)
	}
}

func TestMergeDeleteVersusEditConflicts(t *testing.T) {
	base := workspace("base")
	client := workspace("base")
	delete(client.Projects, "app")
	service := workspace("changed-by-service")
	_, conflicts, err := Merge(base, client, service)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
	if conflicts[0].Path != "projects.app" || string(conflicts[0].Local) != "null" {
		t.Fatalf("conflict: %+v", conflicts[0])
	}
}

func TestMergeConcurrentBranchAddsMergeClaims(t *testing.T) {
	base := workspace("origin")
	project := base.Projects["app"]
	project.Branches = nil
	base.Projects["app"] = project
	client := configClone(t, base)
	service := configClone(t, base)
	project = client.Projects["app"]
	project.Branches = []config.BranchMeta{{Name: "feat/new", Machines: []string{"client"}}}
	client.Projects["app"] = project
	project = service.Projects["app"]
	project.Branches = []config.BranchMeta{{Name: "feat/new", Machines: []string{"service"}}}
	service.Projects["app"] = project
	merged, conflicts, err := Merge(base, client, service)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
	if got := merged.Projects["app"].Branches; len(got) != 1 || !reflect.DeepEqual(got[0].Machines, []string{"client", "service"}) {
		t.Fatalf("branches: %+v", got)
	}
}

func TestMergeConcurrentProjectAddsRemainConflict(t *testing.T) {
	base := workspace("origin")
	client := configClone(t, base)
	service := configClone(t, base)
	client.Projects["new"] = config.Project{Remote: "client"}
	service.Projects["new"] = config.Project{Remote: "service"}
	_, conflicts, err := Merge(base, client, service)
	if err != nil || len(conflicts) != 1 || conflicts[0].Path != "projects.new" {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
}

func TestMergeConflictDoesNotMutateInputs(t *testing.T) {
	base := workspace("base")
	client := workspace("client")
	service := workspace("service")
	before := [][]byte{canonical(t, base), canonical(t, client), canonical(t, service)}
	if _, conflicts, err := Merge(base, client, service); err != nil || len(conflicts) == 0 {
		t.Fatalf("merge: %v %+v", err, conflicts)
	}
	after := [][]byte{canonical(t, base), canonical(t, client), canonical(t, service)}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("inputs mutated")
	}
}

func configClone(t *testing.T, ws *config.Workspace) *config.Workspace {
	t.Helper()
	encoded := canonical(t, ws)
	cloned, err := config.DecodeCanonicalWorkspace(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}
