package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/testutil"
)

func TestBuildPlanStatesOrderTargetsAndDeduplication(t *testing.T) {
	root := t.TempDir()
	sharedRemote := testutil.InitFakeRemote(t, "shared", "main")
	if err := os.MkdirAll(filepath.Join(root, "personal", "present.bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.InitFakePlainCheckout(t, filepath.Join(root, "personal"), "plain", []string{"main"})
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace := &config.Workspace{Projects: map[string]config.Project{
		"z-missing": activeProject(sharedRemote, "personal/missing"),
		"a-present": activeProject(sharedRemote, "personal/present"),
		"m-plain":   activeProject(sharedRemote, "personal/plain"),
		"b-blocked": activeProject(sharedRemote, "blocked"),
		"archived":  {Remote: sharedRemote, Path: "personal/archived", Status: config.StatusArchived},
	}}
	present := workspace.Projects["a-present"]
	present.Mirrors = map[string]string{"same": sharedRemote, "other": "ssh://git@codeberg.org/acme/project.git"}
	workspace.Projects["a-present"] = present

	plan := BuildPlan(root, workspace)
	if got, want := projectNames(plan.Projects), []string{"a-present", "b-blocked", "m-plain", "z-missing"}; !equalStrings(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
	wantStates := []ProjectState{ProjectPresent, ProjectBlocked, ProjectNeedsMigration, ProjectMissing}
	for index, want := range wantStates {
		if got := plan.Projects[index].State; got != want {
			t.Errorf("%s state = %s, want %s", plan.Projects[index].Name, got, want)
		}
	}
	if got := countEndpointURL(plan, sharedRemote); got != 1 {
		t.Fatalf("shared URL endpoints = %d, want 1", got)
	}
	endpoint := endpointForURL(t, plan, sharedRemote)
	if len(endpoint.TargetIDs) != 5 {
		t.Fatalf("shared endpoint target roles = %v, want four origins and one mirror", endpoint.TargetIDs)
	}
	if len(plan.SourceGroups) != 2 {
		t.Fatalf("source groups = %+v, want local and Codeberg SSH groups", plan.SourceGroups)
	}
}

func TestBuildPlanKeepsRelativeRemotesWithDifferentBasesSeparate(t *testing.T) {
	root := t.TempDir()
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"alpha": activeProject("../remote.git", "alpha/project"),
		"beta":  activeProject("../remote.git", "beta/project"),
	}}
	for _, project := range workspace.Projects {
		if err := os.MkdirAll(filepath.Join(root, project.Path)+".bare", 0o755); err != nil {
			t.Fatal(err)
		}
	}

	plan := BuildPlan(root, workspace)
	if len(plan.Endpoints) != 2 {
		t.Fatalf("endpoints = %+v, want distinct relative remote bases", plan.Endpoints)
	}
	if plan.Projects[0].OriginURL == plan.Projects[1].OriginURL {
		t.Fatalf("relative remotes resolved to same URL %q", plan.Projects[0].OriginURL)
	}
}

func TestBuildPlanResolvesMissingProjectRemoteFromWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	workspace := &config.Workspace{Projects: map[string]config.Project{
		"project": activeProject("remotes/project.git", "personal/project"),
	}}

	plan := BuildPlan(root, workspace)
	want := filepath.Join(root, "remotes", "project.git")
	if got := plan.Projects[0].OriginURL; got != want {
		t.Fatalf("missing project origin URL = %q, want %q", got, want)
	}
}

func activeProject(remote, path string) config.Project {
	return config.Project{Remote: remote, Path: path, Status: config.StatusActive, Category: config.CategoryPersonal, DefaultBranch: "main"}
}

func projectNames(projects []ProjectPlan) []string {
	names := make([]string, len(projects))
	for index, project := range projects {
		names[index] = project.Name
	}
	return names
}

func countEndpointURL(plan Plan, url string) int {
	count := 0
	for _, endpoint := range plan.Endpoints {
		if endpoint.URL == url {
			count++
		}
	}
	return count
}

func endpointForURL(t *testing.T, plan Plan, url string) Endpoint {
	t.Helper()
	for _, endpoint := range plan.Endpoints {
		if endpoint.URL == url {
			return endpoint
		}
	}
	t.Fatalf("endpoint %q not found", url)
	return Endpoint{}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
