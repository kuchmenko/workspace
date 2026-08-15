package registry

import (
	"encoding/json"
	"sort"

	"github.com/kuchmenko/workspace/internal/config"
)

type snapshot struct {
	Version  int                        `json:"version"`
	Agent    config.AgentConfig         `json:"agent"`
	Groups   map[string]config.Group    `json:"groups"`
	Projects map[string]snapshotProject `json:"projects"`
	Aliases  map[string]string          `json:"aliases"`
}

type snapshotProject struct {
	Remote        string                    `json:"remote"`
	Mirrors       map[string]string         `json:"mirrors"`
	Path          string                    `json:"path"`
	Status        config.Status             `json:"status"`
	Category      config.Category           `json:"category"`
	Group         string                    `json:"group"`
	DefaultBranch string                    `json:"default_branch"`
	Favorite      bool                      `json:"favorite"`
	Branches      map[string]snapshotBranch `json:"branches"`
}

type snapshotBranch struct {
	Machines   map[string]bool     `json:"machines"`
	LastActive snapshotObservation `json:"last_active"`
	LastPushed snapshotObservation `json:"last_pushed"`
	Created    snapshotObservation `json:"created"`
}

type snapshotObservation struct {
	Machine string `json:"machine"`
	At      string `json:"at"`
}

func encodeSnapshot(workspace *config.Workspace) ([]byte, error) {
	projects := make(map[string]snapshotProject, len(workspace.Projects))
	for name, project := range workspace.Projects {
		branches := make(map[string]snapshotBranch, len(project.Branches))
		for _, branch := range project.Branches {
			machines := make(map[string]bool, len(branch.Machines))
			for _, machine := range branch.Machines {
				machines[machine] = true
			}
			branches[branch.Name] = snapshotBranch{
				Machines:   machines,
				LastActive: snapshotObservation{Machine: branch.LastActiveMachine, At: branch.LastActiveAt},
				LastPushed: snapshotObservation{Machine: branch.LastPushedMachine, At: branch.LastPushedAt},
				Created:    snapshotObservation{Machine: branch.CreatedBy, At: branch.CreatedAt},
			}
		}
		projects[name] = snapshotProject{
			Remote:        project.Remote,
			Mirrors:       copyStrings(project.Mirrors),
			Path:          project.Path,
			Status:        project.Status,
			Category:      project.Category,
			Group:         project.Group,
			DefaultBranch: project.DefaultBranch,
			Favorite:      project.Favorite,
			Branches:      branches,
		}
	}
	return json.Marshal(snapshot{
		Version:  workspace.Meta.Version,
		Agent:    workspace.Agent,
		Groups:   copyGroups(workspace.Groups),
		Projects: projects,
		Aliases:  copyStrings(workspace.Aliases),
	})
}

func decodeSnapshot(body []byte) (*config.Workspace, error) {
	var stored snapshot
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, err
	}
	projects := make(map[string]config.Project, len(stored.Projects))
	for name, project := range stored.Projects {
		branches := make([]config.BranchMeta, 0, len(project.Branches))
		for branchName, branch := range project.Branches {
			machines := make([]string, 0, len(branch.Machines))
			for machine, present := range branch.Machines {
				if present {
					machines = append(machines, machine)
				}
			}
			sort.Strings(machines)
			branches = append(branches, config.BranchMeta{
				Name:              branchName,
				Machines:          machines,
				LastActiveMachine: branch.LastActive.Machine,
				LastActiveAt:      branch.LastActive.At,
				LastPushedMachine: branch.LastPushed.Machine,
				LastPushedAt:      branch.LastPushed.At,
				CreatedBy:         branch.Created.Machine,
				CreatedAt:         branch.Created.At,
			})
		}
		sort.Slice(branches, func(left, right int) bool { return branches[left].Name < branches[right].Name })
		projects[name] = config.Project{
			Remote:        project.Remote,
			Mirrors:       copyStrings(project.Mirrors),
			Path:          project.Path,
			Status:        project.Status,
			Category:      project.Category,
			Group:         project.Group,
			DefaultBranch: project.DefaultBranch,
			Favorite:      project.Favorite,
			Branches:      branches,
		}
	}
	workspace := &config.Workspace{
		Meta:     config.Meta{Version: stored.Version},
		Agent:    stored.Agent,
		Groups:   copyGroups(stored.Groups),
		Projects: projects,
		Aliases:  copyStrings(stored.Aliases),
	}
	if _, err := config.EncodeWorkspace(workspace); err != nil {
		return nil, err
	}
	return workspace, nil
}

func copyStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func copyGroups(source map[string]config.Group) map[string]config.Group {
	result := make(map[string]config.Group, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
