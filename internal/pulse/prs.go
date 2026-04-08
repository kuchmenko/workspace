package pulse

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

// PRScope is which "my PRs" filter to apply on the GitHub Search API.
// Mine = author:@me, Reviewing = review-requested:@me. Pulse keeps the
// two snapshots cached separately so the TUI can flip between sub-tabs
// without an extra HTTP roundtrip per switch.
type PRScope int

const (
	PRScopeMine PRScope = iota
	PRScopeReviewing
)

func (s PRScope) String() string {
	switch s {
	case PRScopeMine:
		return "Mine"
	case PRScopeReviewing:
		return "Reviewing"
	}
	return "?"
}

// PR is a single GitHub pull request as pulse exposes it. Machine is
// resolved through the same wt/<machine>/<topic> + autopush.owned
// pipeline used for commits, applied to the PR's head ref.
type PR struct {
	Org     string // owner
	Repo    string // owner/repo
	Number  int
	NodeID  string // GraphQL node_id, required for draft toggle mutation
	Title   string
	State   string // "open" | "closed"
	Draft   bool
	Author  string // login
	HeadRef string // branch the PR was opened from
	Machine string // resolved owning machine; "shared" if unknown
	URL     string // html_url
	Updated time.Time
	Project string // workspace.toml project name; "" if untracked
	Source  Source // which fetcher produced this record
}

// PRGroup is a flat per-repo bucket inside a PRSnapshot. The TUI
// renders these as a two-level tree (org → repo → PR), but the data
// is kept flat to keep aggregation simple.
type PRGroup struct {
	Org      string
	Repo     string  // owner/repo
	Project  string  // ws.toml project name; "" if untracked
	PRs      []PR
}

// PRSnapshot is the result of one PR fetch pass for one scope.
type PRSnapshot struct {
	Scope       PRScope
	GeneratedAt time.Time
	Total       int
	Groups      []PRGroup
	CollectedIn time.Duration
	// LimitedToWorkspace is true when groups have been filtered to
	// repos present in workspace.toml. The TUI can show this as a
	// hint so the user understands why a PR they expected isn't here.
	LimitedToWorkspace bool

	// Per-source raw counts (before dedupe and filter).
	AppPRCount  int
	GhPRCount   int
	GhAvailable bool
}

// rawSearchResp is GitHub's /search/issues envelope. We project just
// the fields pulse uses; the API returns ~50 more per item.
type rawSearchResp struct {
	TotalCount int            `json:"total_count"`
	Items      []rawSearchPR `json:"items"`
}

type rawSearchPR struct {
	Number    int    `json:"number"`
	NodeID    string `json:"node_id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Draft     bool   `json:"draft"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest struct {
		URL string `json:"url"` // api.github.com/repos/o/r/pulls/N
	} `json:"pull_request"`
	RepositoryURL string `json:"repository_url"` // api.github.com/repos/o/r
	source        Source // populated by the fetcher
}

// rawPRDetail is the response from /repos/{}/pulls/{} which carries
// the head ref pulse needs for machine resolution. Search API does
// not include head, so we do one detail fetch per PR. Cheap because
// total PR count is usually <50 across all of someone's repos.
type rawPRDetail struct {
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// FetchPRs queries the GitHub Search API for PRs matching scope,
// optionally filtered to repos registered in ws.Projects. Each PR is
// then enriched with its head ref via a follow-up detail call so
// machine attribution can run.
//
// Returns a fully populated PRSnapshot. Errors are returned only for
// total failure (auth missing, network down) — partial enrichment
// failures degrade gracefully (Machine = "shared", Author = login).
func FetchPRs(ws *config.Workspace, scope PRScope, limitToWorkspace bool) (PRSnapshot, error) {
	start := time.Now()
	snap := PRSnapshot{
		Scope:              scope,
		GeneratedAt:        start,
		LimitedToWorkspace: limitToWorkspace,
	}

	query := buildSearchQuery(scope)
	items, appCount, ghCount, ghOK, err := fetchPRsSearchBoth(query)
	if err != nil {
		return snap, err
	}
	snap.AppPRCount = appCount
	snap.GhPRCount = ghCount
	snap.GhAvailable = ghOK

	// One token is needed for the head-ref enrichment fallback.
	// Failing to load it is non-fatal — fetchPRHeadAny degrades to
	// gh-only when token is empty.
	token, _ := resolveToken()
	idx := buildProjectIndex(ws.Projects)

	prs := make([]PR, 0, len(items))
	for _, it := range items {
		fullName := repoFullNameFromURL(it.RepositoryURL)
		project := idx.resolveProject(fullName)
		if limitToWorkspace && project == "" {
			continue
		}
		head := fetchPRHeadAny(token, it.PullRequest.URL)
		machine := idx.resolveMachine(project, head)
		owner, _, _ := splitFullName(fullName)
		updated, _ := time.Parse(time.RFC3339, it.UpdatedAt)
		prs = append(prs, PR{
			Org:     owner,
			Repo:    fullName,
			Number:  it.Number,
			NodeID:  it.NodeID,
			Title:   it.Title,
			State:   it.State,
			Draft:   it.Draft,
			Author:  it.User.Login,
			HeadRef: head,
			Machine: machine,
			URL:     it.HTMLURL,
			Updated: updated,
			Project: project,
			Source:  it.source,
		})
	}

	snap.Total = len(prs)
	snap.Groups = groupPRs(prs)
	snap.CollectedIn = time.Since(start)
	return snap, nil
}

// buildSearchQuery constructs the q= string for the GitHub Search API.
// Mine + Reviewing both filter to is:pr is:open and exclude drafts
// only when explicitly closed; the TUI shows draft state via a badge,
// not by hiding rows.
func buildSearchQuery(scope PRScope) string {
	switch scope {
	case PRScopeMine:
		return "is:pr is:open author:@me archived:false"
	case PRScopeReviewing:
		return "is:pr is:open review-requested:@me archived:false"
	}
	return "is:pr is:open author:@me"
}

func searchPRs(token, query string) ([]rawSearchPR, error) {
	url := "https://api.github.com/search/issues?per_page=100&q=" + percentEncode(query)
	client := &http.Client{Timeout: 30 * time.Second}
	var all []rawSearchPR
	for url != "" {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github search: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, describeHTTPError("github search", resp.StatusCode, body)
		}
		var page rawSearchResp
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing search response: %w", err)
		}
		all = append(all, page.Items...)
		url = nextPageURL(resp.Header.Get("Link"))
	}
	return all, nil
}

// fetchPRHead pulls just the head.ref field from a PR detail endpoint.
// Returns "" on any failure — caller treats it as unknown machine.
func fetchPRHead(token, prAPIURL string) string {
	if prAPIURL == "" {
		return ""
	}
	req, err := http.NewRequest("GET", prAPIURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var d rawPRDetail
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return ""
	}
	return d.Head.Ref
}

// groupPRs flattens a []PR into per-repo PRGroup buckets, then sorts
// groups by org+repo and PRs within each group by Number desc.
func groupPRs(prs []PR) []PRGroup {
	bucket := make(map[string]*PRGroup)
	for _, p := range prs {
		g, ok := bucket[p.Repo]
		if !ok {
			g = &PRGroup{Org: p.Org, Repo: p.Repo, Project: p.Project}
			bucket[p.Repo] = g
		}
		g.PRs = append(g.PRs, p)
	}
	out := make([]PRGroup, 0, len(bucket))
	for _, g := range bucket {
		sort.Slice(g.PRs, func(i, j int) bool {
			return g.PRs[i].Number > g.PRs[j].Number
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Org != out[j].Org {
			return out[i].Org < out[j].Org
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// repoFullNameFromURL extracts "owner/repo" from a repository_url
// like "https://api.github.com/repos/owner/repo".
func repoFullNameFromURL(url string) string {
	const prefix = "https://api.github.com/repos/"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	return url[len(prefix):]
}

func splitFullName(fullName string) (owner, repo string, ok bool) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// percentEncode is a tiny URL query encoder for the search query
// string. The standard library net/url.QueryEscape is fine but pulls
// in net/url just for one call; this keeps the dependency surface tight.
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'),
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
