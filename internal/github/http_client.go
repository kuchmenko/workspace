package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type httpClient struct {
	token  string
	client *http.Client
}

func NewHTTPClient(token string) Client {
	return &httpClient{
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpClient) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return c.client.Do(req)
}

func (c *httpClient) CurrentUser() (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("fetching user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API /user returned %d", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}
	return user.Login, nil
}

func (c *httpClient) FetchRepos() ([]Repo, error) {
	url := "https://api.github.com/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member"
	var repos []Repo
	err := c.fetchPaged(url, func(body []byte) error {
		var page []rawRepo
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("parsing repos: %w", err)
		}
		for _, r := range page {
			repos = append(repos, rawRepoToRepo(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repos, nil
}

func rawRepoToRepo(r rawRepo) Repo {
	pushed, _ := time.Parse(time.RFC3339, r.PushedAt)
	return Repo{
		Name:        r.Name,
		FullName:    r.FullName,
		Owner:       r.Owner.Login,
		SSHURL:      r.SSHURL,
		Description: r.Description,
		Private:     r.Private,
		Fork:        r.Fork,
		PushedAt:    pushed,
	}
}

func (c *httpClient) FetchActivity(username string) (map[string]int, error) {
	url := fmt.Sprintf("https://api.github.com/users/%s/events?per_page=100", username)
	counts := make(map[string]int)
	_ = c.fetchPaged(url, func(body []byte) error {
		var events []rawEvent
		if err := json.Unmarshal(body, &events); err != nil {
			return nil
		}
		tallyActivityEvents(counts, events)
		return nil
	})
	return counts, nil
}

var activityEventTypes = map[string]bool{
	"PushEvent":              true,
	"PullRequestEvent":       true,
	"PullRequestReviewEvent": true,
	"IssueCommentEvent":      true,
	"CreateEvent":            true,
	"CommitCommentEvent":     true,
}

func tallyActivityEvents(counts map[string]int, events []rawEvent) {
	for _, e := range events {
		if activityEventTypes[e.Type] {
			counts[e.Repo.Name]++
		}
	}
}

func (c *httpClient) fetchPaged(startURL string, onPage func(body []byte) error) error {
	url := startURL
	for url != "" {
		body, hdr, err := c.fetchOnce(url)
		if err != nil {
			return err
		}
		if err := onPage(body); err != nil {
			return err
		}
		url = nextPageURL(hdr.Get("Link"))
	}
	return nil
}

func (c *httpClient) fetchOnce(url string) ([]byte, http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("GitHub API %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return body, resp.Header, nil
}

func nextPageURL(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		if url, ok := extractRelNext(strings.TrimSpace(part)); ok {
			return url
		}
	}
	return ""
}

func extractRelNext(part string) (string, bool) {
	if !strings.Contains(part, `rel="next"`) {
		return "", false
	}
	start := strings.Index(part, "<")
	end := strings.Index(part, ">")
	if start < 0 || end <= start {
		return "", false
	}
	return part[start+1 : end], true
}
