package agent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

func ParseArchiveThreshold(value string) (time.Duration, error) {
	v := strings.TrimSpace(value)
	for _, unit := range []struct {
		s string
		d time.Duration
	}{{"month", 30 * 24 * time.Hour}, {"w", 7 * 24 * time.Hour}, {"d", 24 * time.Hour}, {"h", time.Hour}} {
		if strings.HasSuffix(v, unit.s) {
			n, err := strconv.Atoi(strings.TrimSuffix(v, unit.s))
			if err != nil || n <= 0 || int64(n) > math.MaxInt64/int64(unit.d) {
				return 0, fmt.Errorf("threshold must be a positive integer followed by h, d, w, or month")
			}
			return time.Duration(n) * unit.d, nil
		}
	}
	return 0, fmt.Errorf("threshold must use h, d, w, or month (for example 72h, 1w, 1month)")
}

func protectedBranch(p *Project, branch string) bool {
	return branch == "main" || branch == "master" || branch == "dev" || branch == "live" || branch == p.DefaultBranch
}

type ProjectIdentity struct{ WorkspaceRoot, ProjectID string }

type ProjectArchiveResult struct {
	Succeeded []ProjectIdentity
	Failures  []string
	Failed    int
}

func ArchiveProjects(projects []worktreeCandidate, progress ...func(ProjectIdentity, bool, string)) ProjectArchiveResult {
	by := map[string][]string{}
	for _, p := range projects {
		by[p.WorkspaceRoot] = append(by[p.WorkspaceRoot], p.ProjectID)
	}
	roots := make([]string, 0, len(by))
	for r := range by {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	var result ProjectArchiveResult
	for _, root := range roots {
		ws, err := config.Load(root)
		if err != nil {
			result.Failures = append(result.Failures, root+": "+err.Error())
			result.Failed += len(by[root])
			if len(progress) > 0 {
				for _, id := range by[root] {
					identity := ProjectIdentity{root, id}
					progress[0](identity, true, "")
					progress[0](identity, false, "failed: "+err.Error())
				}
			}
			continue
		}
		var changed []string
		for _, id := range by[root] {
			if len(progress) > 0 {
				progress[0](ProjectIdentity{root, id}, true, "")
			}
			p, ok := ws.Projects[id]
			if !ok {
				result.Failed++
				result.Failures = append(result.Failures, id+": project missing")
				if len(progress) > 0 {
					progress[0](ProjectIdentity{root, id}, false, "failed: project missing")
				}
				continue
			}
			if p.Status == config.StatusArchived {
				if len(progress) > 0 {
					progress[0](ProjectIdentity{root, id}, false, "unchanged: already archived")
				}
				continue
			}
			p.Status = config.StatusArchived
			ws.Projects[id] = p
			changed = append(changed, id)
		}
		if err := config.Save(root, ws); err != nil {
			result.Failures = append(result.Failures, root+": "+err.Error())
			result.Failed += len(changed)
			if len(progress) > 0 {
				for _, id := range changed {
					progress[0](ProjectIdentity{root, id}, false, "failed: "+err.Error())
				}
			}
			continue
		}
		for _, id := range changed {
			identity := ProjectIdentity{root, id}
			result.Succeeded = append(result.Succeeded, identity)
			if len(progress) > 0 {
				progress[0](identity, false, "archived")
			}
		}
	}
	return result
}

func releaseWorktreeOwnership(root, id, branch string) error {
	return releaseWorktreeOwnershipBatch(root, []ownershipRelease{{id, branch}})
}

func releaseWorktreeOwnershipBatch(root string, releases []ownershipRelease) error {
	if len(releases) == 0 {
		return nil
	}
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	ws, err := config.Load(root)
	if err != nil {
		return err
	}
	changed := false
	for _, release := range releases {
		p, ok := ws.Projects[release.id]
		if !ok {
			return fmt.Errorf("project missing")
		}
		if released, _ := p.ReleaseBranch(release.branch, mc.MachineName); released {
			ws.Projects[release.id] = p
			changed = true
		}
	}
	if changed {
		return config.Save(root, ws)
	}
	return nil
}
