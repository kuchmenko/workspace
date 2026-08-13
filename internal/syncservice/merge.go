package syncservice

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

func Merge(base, client, service *config.Workspace) (*config.Workspace, []Conflict, error) {
	if len(base.Validate()) != 0 || len(client.Validate()) != 0 || len(service.Validate()) != 0 {
		return nil, nil, fmt.Errorf("invalid workspace")
	}
	out := &config.Workspace{}
	var conflicts []Conflict
	out.Meta.Version = scalar("meta.version", base.Meta.Version, client.Meta.Version, service.Meta.Version, &conflicts)
	out.Agent = scalar("agent", base.Agent, client.Agent, service.Agent, &conflicts)
	out.Groups = mergeMap("groups", base.Groups, client.Groups, service.Groups, &conflicts, nil, false)
	out.Aliases = mergeMap("aliases", base.Aliases, client.Aliases, service.Aliases, &conflicts, nil, false)
	out.Projects = mergeMap("projects", base.Projects, client.Projects, service.Projects, &conflicts, mergeProject, false)
	if len(conflicts) != 0 {
		sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
		return nil, conflicts, nil
	}
	canonical, err := config.EncodeCanonicalWorkspace(out)
	if err != nil {
		return nil, nil, err
	}
	merged, err := config.DecodeCanonicalWorkspace(canonical)
	return merged, nil, err
}

func mergeProject(path string, base, client, service config.Project, conflicts *[]Conflict) config.Project {
	out := config.Project{}
	out.Remote = scalar(path+".remote", base.Remote, client.Remote, service.Remote, conflicts)
	out.Path = scalar(path+".path", base.Path, client.Path, service.Path, conflicts)
	out.Status = scalar(path+".status", base.Status, client.Status, service.Status, conflicts)
	out.Category = scalar(path+".category", base.Category, client.Category, service.Category, conflicts)
	out.Group = scalar(path+".group", base.Group, client.Group, service.Group, conflicts)
	out.DefaultBranch = scalar(path+".default_branch", base.DefaultBranch, client.DefaultBranch, service.DefaultBranch, conflicts)
	out.Favorite = scalar(path+".favorite", base.Favorite, client.Favorite, service.Favorite, conflicts)
	out.Mirrors = mergeMap(path+".mirrors", base.Mirrors, client.Mirrors, service.Mirrors, conflicts, nil, false)
	out.Branches = mergeBranches(path+".branches", base.Branches, client.Branches, service.Branches, conflicts)
	return out
}

func mergeBranches(path string, base, client, service []config.BranchMeta, conflicts *[]Conflict) []config.BranchMeta {
	merged := mergeMap(path, branchMap(base), branchMap(client), branchMap(service), conflicts, mergeBranch, true)
	out := make([]config.BranchMeta, 0, len(merged))
	for _, branch := range merged {
		out = append(out, branch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func branchMap(in []config.BranchMeta) map[string]config.BranchMeta {
	out := make(map[string]config.BranchMeta, len(in))
	for _, branch := range in {
		out[branch.Name] = branch
	}
	return out
}

func mergeBranch(path string, base, client, service config.BranchMeta, conflicts *[]Conflict) config.BranchMeta {
	out := config.BranchMeta{Name: client.Name}
	out.Machines = mergeSet(base.Machines, client.Machines, service.Machines)
	out.LastActiveMachine, out.LastActiveAt = observation(base.LastActiveMachine, base.LastActiveAt, client.LastActiveMachine, client.LastActiveAt, service.LastActiveMachine, service.LastActiveAt)
	out.LastPushedMachine, out.LastPushedAt = observation(base.LastPushedMachine, base.LastPushedAt, client.LastPushedMachine, client.LastPushedAt, service.LastPushedMachine, service.LastPushedAt)
	baseCreated := [2]string{base.CreatedBy, base.CreatedAt}
	clientCreated := [2]string{client.CreatedBy, client.CreatedAt}
	serviceCreated := [2]string{service.CreatedBy, service.CreatedAt}
	created := scalar(path+".created", baseCreated, clientCreated, serviceCreated, conflicts)
	out.CreatedBy, out.CreatedAt = created[0], created[1]
	return out
}

func observation(baseBy, baseAt, clientBy, clientAt, serviceBy, serviceAt string) (string, string) {
	base := [2]string{baseBy, baseAt}
	client := [2]string{clientBy, clientAt}
	service := [2]string{serviceBy, serviceAt}
	if client == service {
		return client[0], client[1]
	}
	if client == base {
		return service[0], service[1]
	}
	if service == base {
		return client[0], client[1]
	}
	clientTime, _ := time.Parse(time.RFC3339, clientAt)
	serviceTime, _ := time.Parse(time.RFC3339, serviceAt)
	if clientTime.After(serviceTime) || (clientTime.Equal(serviceTime) && clientBy > serviceBy) {
		return clientBy, clientAt
	}
	return serviceBy, serviceAt
}

func mergeSet(base, client, service []string) []string {
	baseSet, clientSet, serviceSet := set(base), set(client), set(service)
	out := map[string]bool{}
	for key := range baseSet {
		if clientSet[key] && serviceSet[key] {
			out[key] = true
		}
	}
	for key := range clientSet {
		if !baseSet[key] {
			out[key] = true
		}
	}
	for key := range serviceSet {
		if !baseSet[key] {
			out[key] = true
		}
	}
	values := make([]string, 0, len(out))
	for key := range out {
		values = append(values, key)
	}
	sort.Strings(values)
	return values
}

func set(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func scalar[T any](path string, base, client, service T, conflicts *[]Conflict) T {
	if reflect.DeepEqual(client, service) {
		return client
	}
	if reflect.DeepEqual(client, base) {
		return service
	}
	if reflect.DeepEqual(service, base) {
		return client
	}
	*conflicts = append(*conflicts, conflict(path, base, client, service))
	return client
}

func mergeMap[T any](path string, base, client, service map[string]T, conflicts *[]Conflict, merge func(string, T, T, T, *[]Conflict) T, mergeConcurrentAdds bool) map[string]T {
	out := map[string]T{}
	for key := range mapKeys(base, client, service) {
		value, include := mergeMapValue(path+"."+key, base, client, service, key, conflicts, merge, mergeConcurrentAdds)
		if include {
			out[key] = value
		}
	}
	return out
}

func mapKeys[T any](maps ...map[string]T) map[string]bool {
	keys := map[string]bool{}
	for _, values := range maps {
		for key := range values {
			keys[key] = true
		}
	}
	return keys
}

func mergeMapValue[T any](path string, base, client, service map[string]T, key string, conflicts *[]Conflict, merge func(string, T, T, T, *[]Conflict) T, mergeConcurrentAdds bool) (T, bool) {
	baseValue, baseOK := base[key]
	clientValue, clientOK := client[key]
	serviceValue, serviceOK := service[key]
	if !baseOK {
		return mergeAddedMapValue(path, baseValue, clientValue, clientOK, serviceValue, serviceOK, conflicts, merge, mergeConcurrentAdds)
	}
	switch {
	case !clientOK && !serviceOK:
		return clientValue, false
	case !clientOK && reflect.DeepEqual(serviceValue, baseValue):
		return clientValue, false
	case !serviceOK && reflect.DeepEqual(clientValue, baseValue):
		return clientValue, false
	case !clientOK:
		*conflicts = append(*conflicts, conflict(path, baseValue, nil, serviceValue))
		return clientValue, false
	case !serviceOK:
		*conflicts = append(*conflicts, conflict(path, baseValue, clientValue, nil))
		return clientValue, false
	case merge != nil:
		return merge(path, baseValue, clientValue, serviceValue, conflicts), true
	default:
		return scalar(path, baseValue, clientValue, serviceValue, conflicts), true
	}
}

func mergeAddedMapValue[T any](path string, baseValue, clientValue T, clientOK bool, serviceValue T, serviceOK bool, conflicts *[]Conflict, merge func(string, T, T, T, *[]Conflict) T, mergeConcurrentAdds bool) (T, bool) {
	switch {
	case clientOK && serviceOK && reflect.DeepEqual(clientValue, serviceValue):
		return clientValue, true
	case clientOK && serviceOK && mergeConcurrentAdds:
		return merge(path, baseValue, clientValue, serviceValue, conflicts), true
	case clientOK && serviceOK:
		*conflicts = append(*conflicts, conflict(path, nil, clientValue, serviceValue))
		return clientValue, false
	case clientOK:
		return clientValue, true
	case serviceOK:
		return serviceValue, true
	default:
		return clientValue, false
	}
}

func conflict(path string, base, client, service any) Conflict {
	baseJSON, _ := json.Marshal(base)
	clientJSON, _ := json.Marshal(client)
	serviceJSON, _ := json.Marshal(service)
	return Conflict{Path: path, Base: baseJSON, Local: clientJSON, Remote: serviceJSON}
}
