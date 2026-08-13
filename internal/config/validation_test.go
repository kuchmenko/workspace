package config

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestValidateObservationTuplesAndTimestamps(t *testing.T) {
	ws := &Workspace{Projects: map[string]Project{"app": {Branches: []BranchMeta{{
		Name:              "feat/x",
		LastActiveMachine: "linux",
		LastPushedAt:      "not-a-time",
		CreatedBy:         "archlinux",
	}}}}}
	want := []ValidationIssue{
		{Kind: ValidationIncompleteObservation, Project: "app", Branch: "feat/x", Detail: "created_by requires created_at"},
		{Kind: ValidationIncompleteObservation, Project: "app", Branch: "feat/x", Detail: "last_active_machine requires last_active_at"},
		{Kind: ValidationIncompleteObservation, Project: "app", Branch: "feat/x", Detail: "last_pushed_at requires last_pushed_machine"},
		{Kind: ValidationInvalidTimestamp, Project: "app", Branch: "feat/x", Detail: "last_pushed_at is not RFC3339"},
	}
	if got := ws.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("issues:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestDecodeWorkspaceRejectsUnknownFields(t *testing.T) {
	_, err := DecodeWorkspace([]byte("[meta]\nversion = 1\ntyop = true\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown fields: [meta.tyop]") {
		t.Fatalf("error = %v", err)
	}
}
