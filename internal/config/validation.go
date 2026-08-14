package config

import (
	"fmt"
	"sort"
	"time"
)

type ValidationKind string

const (
	ValidationDuplicateBranch       ValidationKind = "duplicate-branch"
	ValidationInvalidTimestamp      ValidationKind = "invalid-timestamp"
	ValidationIncompleteObservation ValidationKind = "incomplete-observation"
)

type ValidationIssue struct {
	Kind    ValidationKind
	Project string
	Branch  string
	Detail  string
}

type branchObservation struct {
	name string
	by   string
	at   string
}

func (w *Workspace) Validate() []ValidationIssue {
	var issues []ValidationIssue
	for projName, proj := range w.Projects {
		issues = append(issues, duplicateBranchIssues(projName, proj.Branches)...)
		issues = append(issues, timestampIssues(projName, proj.Branches)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Project != issues[j].Project {
			return issues[i].Project < issues[j].Project
		}
		if issues[i].Branch != issues[j].Branch {
			return issues[i].Branch < issues[j].Branch
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].Detail < issues[j].Detail
	})
	return issues
}

func timestampIssues(project string, branches []BranchMeta) []ValidationIssue {
	var issues []ValidationIssue
	for _, branch := range branches {
		observations := []branchObservation{
			{"last_active", branch.LastActiveMachine, branch.LastActiveAt},
			{"last_pushed", branch.LastPushedMachine, branch.LastPushedAt},
			{"created", branch.CreatedBy, branch.CreatedAt},
		}
		for _, observation := range observations {
			issues = append(issues, validateObservation(project, branch.Name, observation)...)
		}
	}
	return issues
}

func validateObservation(project, branch string, observation branchObservation) []ValidationIssue {
	var issues []ValidationIssue
	if observation.by == "" && observation.at != "" {
		issues = append(issues, ValidationIssue{Kind: ValidationIncompleteObservation, Project: project, Branch: branch, Detail: fmt.Sprintf("%s_at requires %s", observation.name, observationByField(observation.name))})
	}
	if observation.by != "" && observation.at == "" {
		issues = append(issues, ValidationIssue{Kind: ValidationIncompleteObservation, Project: project, Branch: branch, Detail: fmt.Sprintf("%s requires %s_at", observationByField(observation.name), observation.name)})
	}
	if observation.at != "" {
		if _, err := time.Parse(time.RFC3339, observation.at); err != nil {
			issues = append(issues, ValidationIssue{Kind: ValidationInvalidTimestamp, Project: project, Branch: branch, Detail: fmt.Sprintf("%s_at is not RFC3339", observation.name)})
		}
	}
	return issues
}

func observationByField(name string) string {
	if name == "created" {
		return "created_by"
	}
	return name + "_machine"
}

func duplicateBranchIssues(projName string, branches []BranchMeta) []ValidationIssue {
	seen := make(map[string]int, len(branches))
	var out []ValidationIssue
	for _, b := range branches {
		if b.Name == "" {
			continue
		}
		prev, isDup := seen[b.Name]
		if !isDup {
			seen[b.Name] = len(seen)
			continue
		}
		out = append(out, ValidationIssue{
			Kind:    ValidationDuplicateBranch,
			Project: projName,
			Branch:  b.Name,
			Detail:  fmt.Sprintf("branch %q has %d entries (first at index %d)", b.Name, prev+1, prev),
		})
	}
	return out
}
