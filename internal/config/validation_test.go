package config

import "testing"

func TestValidate_DetectsDuplicateBranchNames(t *testing.T) {
	ws := &Workspace{
		Projects: map[string]Project{
			"app": {
				Branches: []BranchMeta{
					{Name: "feat/foo", Machines: []string{"linux"}},
					{Name: "feat/bar", Machines: []string{"archlinux"}},
					{Name: "feat/foo", Machines: []string{"archlinux"}},
				},
			},
			"lib": {
				Branches: []BranchMeta{
					{Name: "feat/x", Machines: []string{"linux"}},
				},
			},
		},
	}
	issues := ws.Validate()
	if len(issues) != 1 {
		t.Fatalf("want 1 duplicate issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Project != "app" || issues[0].Branch != "feat/foo" || issues[0].Kind != ValidationDuplicateBranch {
		t.Errorf("unexpected issue: %+v", issues[0])
	}
}

func TestValidate_NoDuplicates_ReturnsEmpty(t *testing.T) {
	ws := &Workspace{
		Projects: map[string]Project{
			"app": {Branches: []BranchMeta{
				{Name: "a", Machines: []string{"linux"}},
				{Name: "b", Machines: []string{"linux"}},
			}},
		},
	}
	if got := ws.Validate(); len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}
