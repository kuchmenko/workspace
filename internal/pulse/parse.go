package pulse

import (
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

// projectIndex maps GitHub repo full_name (owner/repo) → workspace.toml
// project name. Built once per collection from ws.Projects via
// git.ParseOwnerRepo on each project's remote URL.
type projectIndex struct {
	byFullName map[string]string         // "owner/repo" → project name
	byName     map[string]config.Project // project name → Project (for OwnerOf)
}

func buildProjectIndex(projects map[string]config.Project) projectIndex {
	idx := projectIndex{
		byFullName: make(map[string]string, len(projects)),
		byName:     make(map[string]config.Project, len(projects)),
	}
	for name, p := range projects {
		idx.byName[name] = p
		if p.Remote == "" {
			continue
		}
		full := git.ParseOwnerRepo(p.Remote)
		if full != "" {
			// GitHub repo names are case-insensitive; normalize so
			// "Owner/Repo" in workspace.toml matches "owner/repo"
			// from the events / search APIs.
			idx.byFullName[strings.ToLower(full)] = name
		}
	}
	return idx
}

// resolveProject returns the workspace.toml project name that owns the
// GitHub repo full_name, or "" if it's not in workspace.toml. Lookup
// is case-insensitive.
func (idx projectIndex) resolveProject(fullName string) string {
	return idx.byFullName[strings.ToLower(fullName)]
}

// resolveMachine resolves which machine produced an event on `branch`
// in `projectName`. Resolution order:
//
//  1. wt/<machine>/<topic> ref → machine segment is authoritative.
//  2. project.autopush.owned[branch] → recorded owner.
//  3. "shared" — main, untracked branches, merges, branches created
//     outside of ws and never claimed.
func (idx projectIndex) resolveMachine(projectName, branch string) string {
	if m := parseWtMachine(branch); m != "" {
		return m
	}
	if projectName != "" {
		if p, ok := idx.byName[projectName]; ok {
			if owner := p.OwnerOf(branch); owner != "" {
				return owner
			}
		}
	}
	return "shared"
}

// parseWtMachine extracts the machine segment from a wt/<machine>/<topic>
// branch name. Returns "" for any other ref shape.
func parseWtMachine(branch string) string {
	if !strings.HasPrefix(branch, "wt/") {
		return ""
	}
	rest := branch[len("wt/"):]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return ""
	}
	return rest[:slash]
}

// flattenEvents converts raw push events into a flat slice of Commit
// records, one per push event, with project + machine resolved.
//
// IMPORTANT: GET /users/{username}/events returns PushEvents in their
// "compact" form — just head/before/ref, no commits[] array. So pulse
// counts each PushEvent as one unit ("a push"), not as N individual
// commits. The Size field is set when GitHub does include it (some
// shapes do); otherwise defaults to 1. To get the actual commit list
// you'd need a per-push compare-API call, which pulse intentionally
// avoids to keep refresh fast and rate-budget friendly.
//
// Events on repos not in workspace.toml are still emitted (Project =
// ""), so the TUI can surface "untracked repo activity" without
// losing data.
func flattenEvents(events []rawEvent, idx projectIndex) []Commit {
	out := make([]Commit, 0, len(events))
	for _, e := range events {
		if e.Type != "PushEvent" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.CreatedAt)
		if err != nil {
			continue
		}
		branch := strings.TrimPrefix(e.Payload.Ref, "refs/heads/")
		project := idx.resolveProject(e.Repo.Name)
		machine := idx.resolveMachine(project, branch)
		out = append(out, Commit{
			Project:   project,
			Repo:      e.Repo.Name,
			Branch:    branch,
			Machine:   machine,
			SHA:       shortSHA(e.Payload.Head),
			Message:   "", // not available in compact event payload
			Author:    e.Actor.Login,
			Timestamp: ts,
			Source:    e.source,
		})
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
