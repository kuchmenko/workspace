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

// NewHTTPClient creates a GitHub API client using a bearer token.
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
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("fetching user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
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
	for url != "" {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching repos: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("GitHub API /user/repos returned %d: %s", resp.StatusCode, string(body))
		}

		var page []rawRepo
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parsing repos: %w", err)
		}

		for _, r := range page {
			pushed, _ := time.Parse(time.RFC3339, r.PushedAt)
			repos = append(repos, Repo{
				Name:     r.Name,
				FullName: r.FullName,
				Owner:    r.Owner.Login,
				SSHURL:   r.SSHURL,
				Private:  r.Private,
				Fork:     r.Fork,
				PushedAt: pushed,
			})
		}

		url = nextPageURL(resp.Header.Get("Link"))
	}

	return repos, nil
}

func (c *httpClient) FetchActivity(username string) (map[string]int, error) {
	url := fmt.Sprintf("https://api.github.com/users/%s/events?per_page=100", username)

	counts := make(map[string]int)
	for url != "" {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return counts, nil
		}
		resp, err := c.do(req)
		if err != nil {
			return counts, nil
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return counts, nil
		}

		var events []rawEvent
		if err := json.Unmarshal(body, &events); err != nil {
			return counts, nil
		}

		for _, e := range events {
			switch e.Type {
			case "PushEvent", "PullRequestEvent", "PullRequestReviewEvent",
				"IssueCommentEvent", "CreateEvent", "CommitCommentEvent":
				counts[e.Repo.Name]++
			}
		}

		url = nextPageURL(resp.Header.Get("Link"))
	}

	return counts, nil
}

// nextPageURL parses the GitHub Link header and returns the "next" URL, or "" if none.
// Format: <https://api.github.com/...?page=2>; rel="next", <...>; rel="last"
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
