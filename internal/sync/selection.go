package sync

import (
	"fmt"
	"maps"
	"slices"
)

type Selection struct {
	plan        Plan
	probes      ProbeReport
	projects    map[string]bool
	targets     map[string]bool
	conversions map[string]string
}

func NewSelection(plan Plan, probes ProbeReport) Selection {
	selection := Selection{
		plan:        plan,
		probes:      probes,
		projects:    make(map[string]bool),
		targets:     make(map[string]bool),
		conversions: make(map[string]string),
	}
	for _, target := range plan.Targets {
		result, ok := probes.result(target.EndpointID)
		selection.targets[target.ID] = ok && result.Status == ProbeSuccess
	}
	for _, project := range plan.Projects {
		selection.projects[project.Name] = selection.targets[project.OriginID]
		if !selection.projects[project.Name] {
			for _, mirrorID := range project.MirrorIDs {
				selection.targets[mirrorID] = false
			}
		}
	}
	return selection
}

func (s *Selection) ExcludeProject(name string) {
	s.projects[name] = false
	for _, target := range s.plan.Targets {
		if target.Project == name {
			s.targets[target.ID] = false
			delete(s.conversions, target.ID)
		}
	}
}

func (s *Selection) IncludeProject(name string) error {
	project, ok := projectPlan(s.plan, name)
	if !ok {
		return fmt.Errorf("unknown project %q", name)
	}
	if err := s.IncludeTarget(project.OriginID); err != nil {
		return err
	}
	s.projects[name] = true
	for _, targetID := range project.MirrorIDs {
		_ = s.IncludeTarget(targetID)
	}
	return nil
}

func (s *Selection) ToggleProject(name string) error {
	if s.ProjectSelected(name) {
		s.ExcludeProject(name)
		return nil
	}
	return s.IncludeProject(name)
}

func (s *Selection) ExcludeSource(sourceKey string) {
	for _, target := range s.plan.Targets {
		if target.SourceKey == sourceKey && target.Role == TargetProjectOrigin {
			s.ExcludeProject(target.Project)
		}
	}
	for _, target := range s.plan.Targets {
		if target.SourceKey == sourceKey {
			s.targets[target.ID] = false
			delete(s.conversions, target.ID)
		}
	}
}

func (s *Selection) IncludeSource(sourceKey string) error {
	foundOrigins, selectedOrigins := s.includeSourceTargets(sourceKey, true)
	foundOthers, selectedOthers := s.includeSourceTargets(sourceKey, false)
	found := foundOrigins || foundOthers
	selectable := selectedOrigins || selectedOthers
	if !found {
		return fmt.Errorf("unknown source %q", sourceKey)
	}
	if !selectable {
		return fmt.Errorf("source %q has no accessible targets", sourceKey)
	}
	return nil
}

func (s *Selection) includeSourceTargets(sourceKey string, origins bool) (bool, bool) {
	found := false
	selected := false
	for _, target := range s.plan.Targets {
		if target.SourceKey != sourceKey || (target.Role == TargetProjectOrigin) != origins {
			continue
		}
		found = true
		if s.IncludeTarget(target.ID) == nil {
			selected = true
		}
	}
	return found, selected
}

func (s *Selection) ToggleSource(sourceKey string) error {
	if s.SourceSelected(sourceKey) {
		s.ExcludeSource(sourceKey)
		return nil
	}
	return s.IncludeSource(sourceKey)
}

func (s *Selection) ExcludeTarget(targetID string) {
	if targetID == s.plan.WorkspaceTargetID && s.plan.ServiceWorkspaceID != "" {
		for _, selected := range s.projects {
			if selected {
				return
			}
		}
	}
	if target, ok := s.target(targetID); ok && target.Role == TargetProjectOrigin {
		s.ExcludeProject(target.Project)
		return
	}
	s.targets[targetID] = false
	delete(s.conversions, targetID)
}

func (s *Selection) IncludeTarget(targetID string) error {
	target, ok := s.target(targetID)
	if !ok {
		return fmt.Errorf("unknown target %q", targetID)
	}
	result, ok := s.probes.result(target.EndpointID)
	if !ok || result.Status != ProbeSuccess {
		if candidate, converted := s.conversions[targetID]; !converted || !s.verifiedConversion(targetID, candidate) {
			return fmt.Errorf("target %q did not pass preflight", targetID)
		}
	}
	if target.Role == TargetMirror && !s.ProjectSelected(target.Project) {
		return fmt.Errorf("mirror %q belongs to an excluded project", targetID)
	}
	s.targets[targetID] = true
	if target.Role == TargetProjectOrigin {
		s.projects[target.Project] = true
	}
	return nil
}

func (s *Selection) ToggleTarget(targetID string) error {
	if s.TargetSelected(targetID) {
		s.ExcludeTarget(targetID)
		return nil
	}
	return s.IncludeTarget(targetID)
}

func (s *Selection) SelectConversion(targetID string) error {
	target, ok := s.target(targetID)
	if !ok {
		return fmt.Errorf("unknown conversion target %q", targetID)
	}
	if target.Role == TargetMirror {
		return fmt.Errorf("mirror %q is not an origin conversion target", targetID)
	}
	result, ok := s.probes.result(target.EndpointID)
	if !ok || result.Candidate == "" || result.CandidateStatus != ProbeSuccess {
		return fmt.Errorf("target %q has no verified SSH conversion", targetID)
	}
	s.conversions[targetID] = result.Candidate
	s.targets[targetID] = true
	if target.Role == TargetProjectOrigin {
		return s.IncludeProject(target.Project)
	}
	return nil
}

func (s *Selection) RemoveConversion(targetID string) {
	delete(s.conversions, targetID)
	s.ExcludeTarget(targetID)
}

func (s Selection) ProjectSelected(name string) bool {
	return s.projects[name]
}

func (s Selection) TargetSelected(id string) bool {
	target, ok := s.target(id)
	if !ok || !s.targets[id] {
		return false
	}
	return target.Role != TargetMirror || s.ProjectSelected(target.Project)
}

func (s Selection) SourceSelected(sourceKey string) bool {
	found := false
	for _, target := range s.plan.Targets {
		if target.SourceKey != sourceKey || !s.TargetSelectable(target.ID) {
			continue
		}
		found = true
		if !s.TargetSelected(target.ID) {
			return false
		}
	}
	return found
}

func (s Selection) TargetSelectable(targetID string) bool {
	target, ok := s.target(targetID)
	if !ok {
		return false
	}
	result, ok := s.probes.result(target.EndpointID)
	if ok && result.Status == ProbeSuccess {
		return true
	}
	candidate, converted := s.conversions[targetID]
	return converted && s.verifiedConversion(targetID, candidate)
}

func (s Selection) ConversionAvailable(targetID string) bool {
	target, ok := s.target(targetID)
	if !ok || target.Role == TargetMirror || s.plan.ServiceWorkspaceID != "" {
		return false
	}
	result, ok := s.probes.result(target.EndpointID)
	return ok && result.Candidate != "" && result.CandidateStatus == ProbeSuccess
}

func (s Selection) SelectedProjects() []string {
	var selected []string
	for name, enabled := range s.projects {
		if enabled {
			selected = append(selected, name)
		}
	}
	slices.Sort(selected)
	return selected
}

func (s Selection) SelectedTargets() []string {
	var selected []string
	for id, enabled := range s.targets {
		if enabled && s.TargetSelected(id) {
			selected = append(selected, id)
		}
	}
	slices.Sort(selected)
	return selected
}

func (s Selection) Conversions() map[string]string {
	return maps.Clone(s.conversions)
}

func (s Selection) Conversion(targetID string) (string, bool) {
	value, ok := s.conversions[targetID]
	return value, ok
}

func (s Selection) target(id string) (Target, bool) {
	for _, target := range s.plan.Targets {
		if target.ID == id {
			return target, true
		}
	}
	return Target{}, false
}

func (s Selection) verifiedConversion(targetID, candidate string) bool {
	target, ok := s.target(targetID)
	if !ok || target.Role == TargetMirror {
		return false
	}
	result, ok := s.probes.result(target.EndpointID)
	return ok && result.Candidate == candidate && result.CandidateStatus == ProbeSuccess
}
