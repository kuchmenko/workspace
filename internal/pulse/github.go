package pulse

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/auth"
)

// resolveToken returns the ws OAuth token. Pulse intentionally does
// not fall back to `gh` CLI: this project has its own GitHub OAuth
// App (see internal/auth/device_flow.go) and `ws auth login` is the
// single source of truth. Mixing two auth paths makes scope/expiry
// debugging much harder.
func resolveToken() (string, error) {
	t, err := auth.LoadToken()
	if err != nil || t.AccessToken == "" {
		return "", fmt.Errorf("no GitHub token: run `ws auth login`")
	}
	return t.AccessToken, nil
}

// describeHTTPError turns a 4xx/5xx body into something the user can
// actually act on. Special-cases 401 because that is the single most
// common pulse failure ("token expired, re-auth").
func describeHTTPError(endpoint string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	switch status {
	case 401:
		return fmt.Errorf("%s: 401 Unauthorized — token expired or missing scopes; run `ws auth login` (or `gh auth refresh -s repo,read:org`). API: %s", endpoint, msg)
	case 403:
		return fmt.Errorf("%s: 403 Forbidden — likely rate-limited or missing scopes. API: %s", endpoint, msg)
	}
	return fmt.Errorf("%s: %d %s", endpoint, status, msg)
}

// rawEvent is a minimal projection of the GitHub Events API response,
// just the fields pulse needs. PushEvent payload on the personal feed
// returns the "compact" shape — head/before/ref/push_id only — never
// the full commits array (that lives on /repos/.../events). So we
// treat each PushEvent as ONE pulse unit ("push"), not as N commits.
// Size carries the commit count GitHub reports for the push so we can
// surface it in the drill-down without an extra compare-API call.
type rawEvent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Actor     struct {
		Login string `json:"login"`
	} `json:"actor"`
	Repo struct {
		Name string `json:"name"` // "owner/repo"
	} `json:"repo"`
	Payload struct {
		Ref          string `json:"ref"` // "refs/heads/wt/linux/foo"
		Head         string `json:"head"`
		Before       string `json:"before"`
		Size         int    `json:"size"`          // commits in this push
		DistinctSize int    `json:"distinct_size"` // unique commits
	} `json:"payload"`
	source Source // populated by the fetcher; not in API JSON
}

// fetchEvents pulls all push events for `username` newer than `since`,
// across as many pages as needed (up to GitHub's 300-event / 10-page
// hard cap). Returns the raw events for parse.go to flatten.
//
// Pagination stops early once a page contains an event older than
// `since` — there is no point asking for more.
func fetchEvents(username string, since time.Time) ([]rawEvent, error) {
	token, err := resolveToken()
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("https://api.github.com/users/%s/events?per_page=100", username)

	var all []rawEvent
	for url != "" {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github events: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, describeHTTPError("github events", resp.StatusCode, body)
		}

		var page []rawEvent
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing events: %w", err)
		}

		stop := false
		for _, e := range page {
			ts, err := time.Parse(time.RFC3339, e.CreatedAt)
			if err != nil {
				continue
			}
			if ts.Before(since) {
				stop = true
				continue
			}
			all = append(all, e)
		}
		if stop {
			break
		}
		url = nextPageURL(resp.Header.Get("Link"))
	}
	return all, nil
}

// fetchUsername resolves the authenticated user's login. Pulse needs
// it to query /users/<me>/events.
func fetchUsername() (string, error) {
	token, err := resolveToken()
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", describeHTTPError("github /user", resp.StatusCode, body)
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// nextPageURL parses GitHub's Link header for the rel="next" URL.
// Duplicated from internal/github to keep pulse decoupled — the
// existing helper there is unexported and the parser is 10 lines.
func nextPageURL(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="next"`) {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}
	return ""
}
