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
			if err != nil || n <= 0 || uint64(n) > uint64(math.MaxInt64)/uint64(unit.d) {
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

func validateDeleteWorktree(p *Project, wt *Worktree) error {
	if err := validateArchiveWorktree(p, wt); err != nil {
		return err
	}
	if protectedBranch(p, wt.Branch) {
		return fmt.Errorf("cannot delete protected branch %s", wt.Branch)
	}
	_, err := refreshWorktreePublication(p, wt)
	return err
}

type ProjectIdentity struct{ WorkspaceRoot, ProjectID string }

type ProjectArchiveResult struct {
	Succeeded []ProjectIdentity
	Failures  []string
}

func ArchiveProjects(projects []worktreeCandidate) ProjectArchiveResult {
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
			continue
		}
		var changed []string
		for _, id := range by[root] {
			p, ok := ws.Projects[id]
			if ok && p.Status != config.StatusArchived {
				p.Status = config.StatusArchived
				ws.Projects[id] = p
				changed = append(changed, id)
			}
		}
		if err := config.Save(root, ws); err != nil {
			result.Failures = append(result.Failures, root+": "+err.Error())
			continue
		}
		for _, id := range changed {
			result.Succeeded = append(result.Succeeded, ProjectIdentity{root, id})
		}
	}
	return result
}

func releaseWorktreeOwnership(root, id, branch string) error {
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	ws, err := config.Load(root)
	if err != nil {
		return err
	}
	p, ok := ws.Projects[id]
	if !ok {
		return fmt.Errorf("project missing")
	}
	if changed, _ := p.ReleaseBranch(branch, mc.MachineName); changed {
		ws.Projects[id] = p
		return config.Save(root, ws)
	}
	return nil
}
