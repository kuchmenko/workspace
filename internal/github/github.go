package github

import (
	"sort"
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

// FetchAll resolves a client, fetches repos and activity, merges them, and returns sorted by activity.
func FetchAll() ([]Repo, string, error) {
	client, err := ResolveClient()
	if err != nil {
		return nil, "", err
	}

	username, err := client.CurrentUser()
	if err != nil {
		return nil, "", err
	}

	repos, err := client.FetchRepos()
	if err != nil {
		return nil, username, err
	}

	activity, _ := client.FetchActivity(username)

	for i := range repos {
		repos[i].Activity = activity[repos[i].FullName]
	}

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
