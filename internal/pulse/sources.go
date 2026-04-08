package pulse

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Pulse pulls from two sources in parallel and merges results:
//
//   1. The ws GitHub App (REQUIRED). This is the canonical source —
//      uses the OAuth user-to-server token from internal/auth and
//      sees whatever orgs / repos the App is installed on.
//
//   2. The gh CLI (OPT-IN). Used only when gh is installed AND the
//      user is logged in. gh's token is independent and often has
//      access to orgs the ws App is not installed on (the common
//      case: a work org where install requires an admin approval
//      that hasn't happened yet). Pulse uses gh as a "fill in the
//      gaps" source.
//
// Records are tagged with their Source so the snapshot can show
// "this came from app, that came from gh", which makes it obvious
// when the answer to "why don't I see X" is "install the App on Y
// org" vs "log in via gh".

// ghAvailable reports whether the local gh CLI is installed and the
// user is currently logged in. Both checks are required: a logged-out
// gh is just as useless as no gh at all.
func ghAvailable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	// `gh auth status` exits 0 only when there's an active token.
	// Suppress its (chatty) stderr — we only care about the exit code.
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// fetchEventsBoth runs the App and gh fetchers in parallel and
// returns a deduped slice of raw events plus per-source counts. The
// App fetcher is mandatory: if it errors, the whole call fails. The
// gh fetcher is best-effort: any error there is logged into the
// returned diagnostics but does not block the main result.
func fetchEventsBoth(username string, since time.Time) (events []rawEvent, appCount, ghCount int, ghOK bool, err error) {
	type result struct {
		ev  []rawEvent
		err error
	}
	var (
		wg                 sync.WaitGroup
		appRes, ghRes      result
		runGh              = ghAvailable()
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		ev, e := fetchEventsApp(username, since)
		for i := range ev {
			ev[i].source = SourceApp
		}
		appRes = result{ev: ev, err: e}
	}()

	if runGh {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev, e := fetchEventsGh(username, since)
			for i := range ev {
				ev[i].source = SourceGh
			}
			ghRes = result{ev: ev, err: e}
		}()
	}
	wg.Wait()

	if appRes.err != nil {
		return nil, 0, 0, runGh, appRes.err
	}
	merged := mergeEvents(appRes.ev, ghRes.ev)
	return merged, len(appRes.ev), len(ghRes.ev), runGh, nil
}

// mergeEvents concatenates two raw event slices and removes
// duplicates by event id. When the same event appears in both
// sources, the App copy wins (it's our source of truth) so the
// machine attribution remains stable across runs.
func mergeEvents(app, gh []rawEvent) []rawEvent {
	seen := make(map[string]bool, len(app)+len(gh))
	out := make([]rawEvent, 0, len(app)+len(gh))
	for _, e := range app {
		if e.ID == "" || !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	for _, e := range gh {
		if e.ID != "" && seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

// fetchEventsApp is the original HTTP path renamed for clarity.
func fetchEventsApp(username string, since time.Time) ([]rawEvent, error) {
	return fetchEvents(username, since)
}

// fetchEventsGh shells out to `gh api --paginate /users/<u>/events`
// and parses the result. The output of `gh api --paginate` for an
// array endpoint is a single concatenated array, so json.Unmarshal
// works directly on it.
func fetchEventsGh(username string, since time.Time) ([]rawEvent, error) {
	path := fmt.Sprintf("/users/%s/events?per_page=100", username)
	cmd := exec.Command("gh", "api", "--paginate", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api events: %w", err)
	}
	var raw []rawEvent
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh api events: %w", err)
	}
	// Apply the same since-filter as the HTTP path so both sources
	// produce comparable slices.
	filtered := raw[:0]
	for _, e := range raw {
		ts, err := time.Parse(time.RFC3339, e.CreatedAt)
		if err != nil {
			continue
		}
		if ts.Before(since) {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

// =============================================================================
// PR sources
// =============================================================================

// fetchPRsSearchBoth runs both PR-search fetchers in parallel and
// returns a deduped slice of raw search results plus per-source
// counts. Same semantics as fetchEventsBoth: app is mandatory, gh
// is best-effort.
func fetchPRsSearchBoth(query string) (items []rawSearchPR, appCount, ghCount int, ghOK bool, err error) {
	type result struct {
		items []rawSearchPR
		err   error
	}
	var (
		wg            sync.WaitGroup
		appRes, ghRes result
		runGh         = ghAvailable()
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		token, terr := resolveToken()
		if terr != nil {
			appRes.err = terr
			return
		}
		items, e := searchPRs(token, query)
		for i := range items {
			items[i].source = SourceApp
		}
		appRes = result{items: items, err: e}
	}()

	if runGh {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, e := searchPRsGh(query)
			for i := range items {
				items[i].source = SourceGh
			}
			ghRes = result{items: items, err: e}
		}()
	}
	wg.Wait()

	if appRes.err != nil {
		return nil, 0, 0, runGh, appRes.err
	}
	merged := mergePRSearchResults(appRes.items, ghRes.items)
	return merged, len(appRes.items), len(ghRes.items), runGh, nil
}

// mergePRSearchResults dedupes by node_id with App-wins precedence.
func mergePRSearchResults(app, gh []rawSearchPR) []rawSearchPR {
	seen := make(map[string]bool, len(app)+len(gh))
	out := make([]rawSearchPR, 0, len(app)+len(gh))
	for _, p := range app {
		if p.NodeID == "" || !seen[p.NodeID] {
			seen[p.NodeID] = true
			out = append(out, p)
		}
	}
	for _, p := range gh {
		if p.NodeID != "" && seen[p.NodeID] {
			continue
		}
		seen[p.NodeID] = true
		out = append(out, p)
	}
	return out
}

// searchPRsGh runs `gh api` on /search/issues with manual pagination
// (search is an object endpoint, --paginate doesn't merge cleanly).
func searchPRsGh(query string) ([]rawSearchPR, error) {
	var all []rawSearchPR
	page := 1
	for {
		path := fmt.Sprintf("/search/issues?per_page=100&page=%d&q=%s", page, percentEncode(query))
		cmd := exec.Command("gh", "api", path)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("gh api search page %d: %w", page, err)
		}
		var resp rawSearchResp
		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("parsing gh api search: %w", err)
		}
		all = append(all, resp.Items...)
		if len(resp.Items) < 100 || len(all) >= resp.TotalCount {
			break
		}
		page++
		if page > 10 {
			break // GitHub search caps at 1000 results / 10 pages
		}
	}
	return all, nil
}

// fetchPRHeadAny picks whichever auth source is more likely to have
// access to a particular repo. Tries App first, falls back to gh on
// any error or empty result. The head ref is needed for machine
// attribution.
func fetchPRHeadAny(token, prAPIURL string) string {
	if h := fetchPRHead(token, prAPIURL); h != "" {
		return h
	}
	if !ghAvailable() {
		return ""
	}
	// Convert https://api.github.com/repos/o/r/pulls/N → /repos/o/r/pulls/N
	const prefix = "https://api.github.com"
	path := strings.TrimPrefix(prAPIURL, prefix)
	if path == prAPIURL {
		return ""
	}
	out, err := exec.Command("gh", "api", path).Output()
	if err != nil {
		return ""
	}
	var d rawPRDetail
	if err := json.Unmarshal(out, &d); err != nil {
		return ""
	}
	return d.Head.Ref
}
