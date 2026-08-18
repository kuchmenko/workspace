package config

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type ValidationKind string

const (
	ValidationDuplicateBranch ValidationKind = "duplicate-branch"
	ValidationRemoteControl   ValidationKind = "remote-control"
)

type ValidationIssue struct {
	Kind    ValidationKind
	Project string
	Branch  string
	Detail  string
}

func (w *Workspace) Validate() []ValidationIssue {
	var issues []ValidationIssue
	for projName, proj := range w.Projects {
		issues = append(issues, duplicateBranchIssues(projName, proj.Branches)...)
		if containsControl(proj.Remote) {
			issues = append(issues, ValidationIssue{Kind: ValidationRemoteControl, Project: projName, Detail: "project remote contains control characters"})
		}
		for _, remote := range proj.Mirrors {
			if containsControl(remote) {
				issues = append(issues, ValidationIssue{Kind: ValidationRemoteControl, Project: projName, Detail: "mirror remote contains control characters"})
			}
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Project != issues[j].Project {
			return issues[i].Project < issues[j].Project
		}
		if issues[i].Branch != issues[j].Branch {
			return issues[i].Branch < issues[j].Branch
		}
		return issues[i].Detail < issues[j].Detail
	})
	return issues
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
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
