package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type ghClient struct{}

// NewGHClient creates a GitHub API client using the gh CLI.
func NewGHClient() Client {
	return &ghClient{}
}

func (c *ghClient) CurrentUser() (string, error) {
	cmd := exec.Command("gh", "api", "/user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh api /user: %w (is gh authenticated?)", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *ghClient) FetchRepos() ([]Repo, error) {
	cmd := exec.Command("gh", "api",
		"/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member",
		"--paginate",
		"--cache", "1h",
		"--jq", ".[]",
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("gh api: %w", err)
	}

	var repos []Repo
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var r rawRepo
		if err := dec.Decode(&r); err != nil {
			continue
		}
		pushed, _ := time.Parse(time.RFC3339, r.PushedAt)
		repos = append(repos, Repo{
			Name:        r.Name,
			FullName:    r.FullName,
			Owner:       r.Owner.Login,
			SSHURL:      r.SSHURL,
			Description: r.Description,
			Private:     r.Private,
			Fork:        r.Fork,
			PushedAt:    pushed,
		})
	}

	return repos, nil
}

func (c *ghClient) FetchActivity(username string) (map[string]int, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("/users/%s/events?per_page=100", username),
		"--paginate",
		"--cache", "1h",
		"--jq", ".[]",
	)
	out, err := cmd.Output()
	if err != nil {
		return map[string]int{}, nil
	}

	counts := make(map[string]int)
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var e rawEvent
		if err := dec.Decode(&e); err != nil {
			continue
		}
		switch e.Type {
		case "PushEvent", "PullRequestEvent", "PullRequestReviewEvent",
			"IssueCommentEvent", "CreateEvent", "CommitCommentEvent":
			counts[e.Repo.Name]++
		}
	}

	return counts, nil
}
