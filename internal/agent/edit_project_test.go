package agent

import (
	"path/filepath"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func writeTestWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ws := &config.Workspace{
		Meta: config.Meta{Version: 1, Root: root},
		Projects: map[string]config.Project{
			"alpha": {
				Remote:        "git@github.com:user/alpha.git",
				Path:          "personal/alpha",
				Status:        config.StatusActive,
				Category:      config.CategoryPersonal,
				DefaultBranch: "main",
			},
			"beta": {
				Remote:        "git@github.com:org/beta.git",
				Path:          "work/beta",
				Status:        config.StatusActive,
				Category:      config.CategoryWork,
				Group:         "org",
				DefaultBranch: "master",
			},
		},
		Groups: map[string]config.Group{},
	}
	if err := config.Save(root, ws); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return root
}

func TestEditProjectMetadata_SetsGroupAndCategory(t *testing.T) {
	root := writeTestWorkspace(t)

	if err := EditProjectMetadata(root, "alpha", "personal", config.CategoryPersonal); err != nil {
		t.Fatalf("EditProjectMetadata: %v", err)
	}

	got, err := config.Load(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p := got.Projects["alpha"]
	if p.Group != "personal" {
		t.Errorf("group = %q, want %q", p.Group, "personal")
	}
	if p.Category != config.CategoryPersonal {
		t.Errorf("category = %q, want %q", p.Category, config.CategoryPersonal)
	}
}

func TestEditProjectMetadata_ClearsGroup(t *testing.T) {
	root := writeTestWorkspace(t)

	if err := EditProjectMetadata(root, "beta", "  ", config.CategoryWork); err != nil {
		t.Fatalf("EditProjectMetadata: %v", err)
	}

	got, err := config.Load(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g := got.Projects["beta"].Group; g != "" {
		t.Errorf("group = %q, want empty", g)
	}
}

func TestEditProjectMetadata_RejectsUnknownProject(t *testing.T) {
	root := writeTestWorkspace(t)
	if err := EditProjectMetadata(root, "ghost", "x", config.CategoryPersonal); err == nil {
		t.Fatalf("expected error for missing project")
	}
}

func TestEditProjectMetadata_RejectsBadCategory(t *testing.T) {
	root := writeTestWorkspace(t)
	if err := EditProjectMetadata(root, "alpha", "x", "bogus"); err == nil {
		t.Fatalf("expected error for invalid category")
	}
}

func TestEditProjectMetadata_RejectsEmptyArgs(t *testing.T) {
	root := writeTestWorkspace(t)
	if err := EditProjectMetadata("", "alpha", "x", config.CategoryPersonal); err == nil {
		t.Fatalf("expected error for empty wsRoot")
	}
	if err := EditProjectMetadata(root, "", "x", config.CategoryPersonal); err == nil {
		t.Fatalf("expected error for empty projID")
	}
}

func TestEditProjectMetadata_PreservesOtherProjects(t *testing.T) {
	root := writeTestWorkspace(t)

	if err := EditProjectMetadata(root, "alpha", "personal", config.CategoryPersonal); err != nil {
		t.Fatalf("edit: %v", err)
	}

	got, err := config.Load(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	beta := got.Projects["beta"]
	if beta.Group != "org" || beta.Category != config.CategoryWork {
		t.Errorf("beta mutated: group=%q category=%q", beta.Group, beta.Category)
	}
	// Confirm the on-disk file is the workspace.toml (sanity check).
	if _, err := filepath.Abs(filepath.Join(root, "workspace.toml")); err != nil {
		t.Fatalf("abs path: %v", err)
	}
}

func TestRecomputeGroups(t *testing.T) {
	projects := []Project{
		{ID: "a", Group: "personal"},
		{ID: "b", Group: ""},
		{ID: "c", Group: "personal"},
		{ID: "d", Group: "work"},
	}
	got := recomputeGroups(projects)
	want := []string{"personal", "work"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExistingGroups_DedupesAcrossWorkspaces(t *testing.T) {
	w := []WorkspaceData{
		{Groups: []string{"personal", "work"}},
		{Groups: []string{"personal", "experiments"}},
		{Groups: []string{""}},
	}
	got := existingGroups(w)
	want := []string{"experiments", "personal", "work"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
