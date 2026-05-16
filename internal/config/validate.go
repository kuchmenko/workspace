package config

import (
	"fmt"
	"sort"
)

// ValidationKind enumerates the structural problems Validate can detect.
type ValidationKind string

const (
	ValidationDuplicateBranch ValidationKind = "duplicate-branch"
)

// ValidationIssue describes one Workspace structural defect found by
// Validate. Callers (notably the reconciler) translate these into
// conflict-store entries (KindBranchDuplicate).
type ValidationIssue struct {
	Kind    ValidationKind
	Project string
	Branch  string
	Detail  string
}

// Validate inspects the in-memory Workspace for structural defects that
// the TOML decoder will not catch on its own — currently: duplicate
// branch names within a project's [[branches]] list, which arise when
// two machines independently add the same branch and union-merge
// concatenates their writes.
func (w *Workspace) Validate() []ValidationIssue {
	var issues []ValidationIssue
	for projName, proj := range w.Projects {
		issues = append(issues, duplicateBranchIssues(projName, proj.Branches)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Project != issues[j].Project {
			return issues[i].Project < issues[j].Project
		}
		return issues[i].Branch < issues[j].Branch
	})
	return issues
}

// duplicateBranchIssues reports duplicate-name [[branches]] entries
// within one project. The first occurrence is tracked silently; every
// subsequent occurrence yields a ValidationIssue.
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
