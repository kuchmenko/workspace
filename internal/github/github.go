package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Repo struct {
	Name     string
	FullName string // owner/repo
	Owner    string
	SSHURL   string
	Private  bool
	Fork     bool
	PushedAt time.Time
	Activity int // event count from Events API (last 90 days)
}

type rawRepo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	SSHURL   string `json:"ssh_url"`
	Private  bool   `json:"private"`
	Fork     bool   `json:"fork"`
	PushedAt string `json:"pushed_at"`
}

type rawEvent struct {
	Type string `json:"type"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
}

// CurrentUser returns the authenticated GitHub username.
func CurrentUser() (string, error) {
	cmd := exec.Command("gh", "api", "/user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh api /user: %w (is gh authenticated?)", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// FetchRepos returns all repos accessible to the authenticated user.
func FetchRepos() ([]Repo, error) {
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
			Name:     r.Name,
			FullName: r.FullName,
			Owner:    r.Owner.Login,
			SSHURL:   r.SSHURL,
			Private:  r.Private,
			Fork:     r.Fork,
			PushedAt: pushed,
		})
	}

	return repos, nil
}

// FetchActivity fetches the user's events and returns a map of full_name → event count.
func FetchActivity(username string) (map[string]int, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("/users/%s/events?per_page=100", username),
		"--paginate",
		"--cache", "1h",
		"--jq", ".[]",
	)
	out, err := cmd.Output()
	if err != nil {
		// Non-fatal: activity is optional
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

// FetchAll fetches repos and activity, merges them, and returns sorted by activity.
func FetchAll() ([]Repo, string, error) {
	username, err := CurrentUser()
	if err != nil {
		return nil, "", err
	}

	repos, err := FetchRepos()
	if err != nil {
		return nil, username, err
	}

	activity, _ := FetchActivity(username)

	for i := range repos {
		repos[i].Activity = activity[repos[i].FullName]
	}

	// Sort: activity desc, then pushed_at desc
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Activity != repos[j].Activity {
			return repos[i].Activity > repos[j].Activity
		}
		return repos[i].PushedAt.After(repos[j].PushedAt)
	})

	return repos, username, nil
}

// Orgs extracts unique org/owner names from repos.
func Orgs(repos []Repo) []string {
	seen := make(map[string]bool)
	var orgs []string
	for _, r := range repos {
		if !seen[r.Owner] {
			seen[r.Owner] = true
			orgs = append(orgs, r.Owner)
		}
	}
	sort.Strings(orgs)
	return orgs
}
