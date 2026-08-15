package sync

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type ProjectState string

const (
	ProjectPresent        ProjectState = "present"
	ProjectMissing        ProjectState = "missing"
	ProjectNeedsMigration ProjectState = "needs-migration"
	ProjectBlocked        ProjectState = "blocked"
)

type TargetRole string

const (
	TargetProjectOrigin TargetRole = "project-origin"
	TargetMirror        TargetRole = "mirror"
)

type ProjectSnapshot struct {
	Remote        string
	Path          string
	Status        config.Status
	DefaultBranch string
	Mirrors       map[string]string
}

type ProjectPlan struct {
	Name       string
	State      ProjectState
	MainPath   string
	BarePath   string
	Diagnostic string
	Snapshot   ProjectSnapshot
	OriginID   string
	OriginURL  string
	MirrorIDs  []string
	MirrorURLs map[string]string
}

type Target struct {
	ID         string
	Role       TargetRole
	Project    string
	Mirror     string
	URL        string
	ConfigURL  string
	Remote     git.Remote
	ParseError string
	EndpointID string
	SourceKey  string
	External   bool
	Executable bool
	Repository string
}

type Endpoint struct {
	ID         string
	URL        string
	Remote     git.Remote
	TargetIDs  []string
	SourceKey  string
	External   bool
	Executable bool
	ParseError string
}

type SourceGroup struct {
	Key         string
	EndpointIDs []string
	TargetIDs   []string
}

type Plan struct {
	Root         string
	Projects     []ProjectPlan
	Targets      []Target
	Endpoints    []Endpoint
	SourceGroups []SourceGroup
}

func BuildPlan(root string, ws *config.Workspace) Plan {
	plan := Plan{Root: root}
	for _, name := range slices.Sorted(maps.Keys(ws.Projects)) {
		project := ws.Projects[name]
		if project.Status != config.StatusActive {
			continue
		}
		plan.addProject(name, project)
	}
	plan.buildEndpoints()
	return plan
}

func (p *Plan) addProject(name string, project config.Project) {
	mainPath, err := layout.ProjectPath(p.Root, project.Path)
	if err != nil {
		p.Projects = append(p.Projects, ProjectPlan{
			Name:       name,
			State:      ProjectBlocked,
			Diagnostic: err.Error(),
			Snapshot:   snapshotProject(project),
		})
		return
	}
	projectPlan := ProjectPlan{
		Name:       name,
		MainPath:   mainPath,
		BarePath:   layout.BarePath(mainPath),
		Snapshot:   snapshotProject(project),
		OriginID:   "project:" + name + ":origin",
		MirrorURLs: make(map[string]string),
	}
	projectPlan.State, projectPlan.Diagnostic = classifyProject(projectPlan.MainPath, projectPlan.BarePath)
	base := projectNetworkBase(p.Root, projectPlan)
	origin := newTarget(projectPlan.OriginID, TargetProjectOrigin, name, "", project.Remote, projectPlan.BarePath, base)
	projectPlan.OriginURL = origin.URL
	p.Targets = append(p.Targets, origin)
	for _, mirror := range slices.Sorted(maps.Keys(project.Mirrors)) {
		id := "project:" + name + ":mirror:" + mirror
		projectPlan.MirrorIDs = append(projectPlan.MirrorIDs, id)
		target := newTarget(id, TargetMirror, name, mirror, project.Mirrors[mirror], projectPlan.BarePath, base)
		projectPlan.MirrorURLs[mirror] = target.URL
		p.Targets = append(p.Targets, target)
	}
	p.Projects = append(p.Projects, projectPlan)
}

func snapshotProject(project config.Project) ProjectSnapshot {
	return ProjectSnapshot{
		Remote:        project.Remote,
		Path:          project.Path,
		Status:        project.Status,
		DefaultBranch: project.DefaultBranch,
		Mirrors:       maps.Clone(project.Mirrors),
	}
}

func classifyProject(mainPath, barePath string) (ProjectState, string) {
	if info, err := os.Stat(barePath); err == nil {
		if info.IsDir() {
			return ProjectPresent, ""
		}
		return ProjectBlocked, barePath + " is not a directory"
	} else if !os.IsNotExist(err) {
		return ProjectBlocked, err.Error()
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		return ProjectMissing, ""
	} else if err != nil {
		return ProjectBlocked, err.Error()
	}
	if git.IsRepo(mainPath) {
		return ProjectNeedsMigration, "plain checkout at " + mainPath
	}
	return ProjectBlocked, "non-repository path at " + mainPath
}

func projectNetworkBase(root string, project ProjectPlan) string {
	switch project.State {
	case ProjectPresent:
		return project.BarePath
	case ProjectNeedsMigration:
		return project.MainPath
	default:
		return root
	}
}

func newTarget(id string, role TargetRole, project, mirror, raw, repository, base string) Target {
	target := Target{ID: id, Role: role, Project: project, Mirror: mirror, URL: raw, ConfigURL: raw, Repository: repository}
	resolved, err := git.ResolveRemoteURL(raw, base)
	if err != nil {
		target.ParseError = err.Error()
		return target
	}
	target.URL = resolved
	remote, err := git.ParseRemote(resolved)
	if err != nil {
		target.ParseError = err.Error()
		return target
	}
	target.Remote = remote
	target.SourceKey = remote.SourceKey()
	target.External = remote.Scheme == "https" || remote.Scheme == "ssh"
	target.Executable = true
	return target
}

func (p *Plan) buildEndpoints() {
	byURL := make(map[string]int)
	for index := range p.Targets {
		target := &p.Targets[index]
		endpointIndex, exists := byURL[target.URL]
		if !exists {
			endpointIndex = len(p.Endpoints)
			byURL[target.URL] = endpointIndex
			p.Endpoints = append(p.Endpoints, Endpoint{
				ID:         fmt.Sprintf("remote:%04d", endpointIndex),
				URL:        target.URL,
				Remote:     target.Remote,
				SourceKey:  target.SourceKey,
				External:   target.External,
				Executable: target.Executable,
				ParseError: target.ParseError,
			})
		}
		endpoint := &p.Endpoints[endpointIndex]
		endpoint.TargetIDs = append(endpoint.TargetIDs, target.ID)
		target.EndpointID = endpoint.ID
	}
	p.buildSourceGroups()
}

func (p *Plan) buildSourceGroups() {
	groups := make(map[string]*SourceGroup)
	for _, endpoint := range p.Endpoints {
		key := endpoint.SourceKey
		if key == "" {
			key = "unsupported"
		}
		group := groups[key]
		if group == nil {
			group = &SourceGroup{Key: key}
			groups[key] = group
		}
		group.EndpointIDs = append(group.EndpointIDs, endpoint.ID)
		group.TargetIDs = append(group.TargetIDs, endpoint.TargetIDs...)
	}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		p.SourceGroups = append(p.SourceGroups, *groups[key])
	}
}
