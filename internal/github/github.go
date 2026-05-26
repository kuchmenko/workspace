package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type GhAppProvider struct{}

func NewGhAppProviderStub() *GhAppProvider { return &GhAppProvider{} }

func (*GhAppProvider) Name() string { return "gh-app" }

func (*GhAppProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotImplemented
}

type cacheFile struct {
	Version  int       `json:"version"`
	StoredAt time.Time `json:"stored_at"`
	Repos    []Repo    `json:"repos"`
}

const (
	cacheVersion = 1

	cacheTTL = time.Hour
)

func CacheTTL() time.Duration { return cacheTTL }

func cachePath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "ws", "github-cache.json"), nil
}

func LoadCache() ([]Repo, time.Duration, error) {
	p, err := cachePath()
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	cf, ok := parseCacheFile(data)
	if !ok {
		return nil, 0, nil
	}
	return cf.Repos, time.Since(cf.StoredAt), nil
}

func parseCacheFile(data []byte) (cacheFile, bool) {
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return cacheFile{}, false
	}
	if cf.Version != cacheVersion {
		return cacheFile{}, false
	}
	if !cacheReposLookSane(cf.Repos) {
		return cacheFile{}, false
	}
	return cf, true
}

func cacheReposLookSane(repos []Repo) bool {
	for _, r := range repos {
		if r.Owner == "" && r.SSHURL == "" {
			return false
		}
	}
	return true
}

func SaveCache(repos []Repo) error {
	if len(repos) == 0 {
		return nil
	}
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{
		Version:  cacheVersion,
		StoredAt: time.Now().UTC(),
		Repos:    repos,
	})
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func PurgeCache() error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func CacheFresh() (bool, time.Duration) {
	_, age, err := LoadCache()
	if err != nil {
		return false, 0
	}
	return age > 0 && age < cacheTTL, age
}

type Client interface {
	CurrentUser() (string, error)
	FetchRepos() ([]Repo, error)
	FetchActivity(username string) (map[string]int, error)
}

type ghClient struct{}

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
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
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

type Repo struct {
	Name        string
	FullName    string
	Owner       string
	SSHURL      string
	Description string
	Private     bool
	Fork        bool
	PushedAt    time.Time
	Activity    int
}

type rawRepo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	SSHURL      string `json:"ssh_url"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	Fork        bool   `json:"fork"`
	PushedAt    string `json:"pushed_at"`
}

type rawEvent struct {
	Type string `json:"type"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
}

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

type Provider interface {
	SuggestRepos(ctx context.Context, limit int) ([]Repo, error)

	Name() string
}

var ErrNotAuthed = errors.New("no GitHub authentication configured")

var ErrNotImplemented = errors.New("not implemented (GitHub App)")

func ResolveProvider() Provider {
	if token, err := loadOAuthToken(); err == nil && token != "" {
		client := NewHTTPClient(token)
		if oauthProbe(client) {
			return &clientProvider{client: client, name: "http-oauth"}
		}
	}

	if ghAuthStatus() {
		return &clientProvider{
			client: NewGHClient(),
			name:   "gh-cli",
		}
	}

	return noopProvider{}
}

func oauthProbeNetwork(c Client) bool {
	done := make(chan bool, 1)
	go func() {
		_, err := c.CurrentUser()
		done <- err == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(2 * time.Second):
		return false
	}
}

var (
	loadOAuthToken = func() (string, error) {
		token, err := LoadToken()
		if err != nil {
			return "", err
		}
		return token.AccessToken, nil
	}

	ghAuthStatus = func() bool {

		cmd := exec.Command("gh", "auth", "status")
		return cmd.Run() == nil
	}

	oauthProbe = oauthProbeNetwork
)

type clientProvider struct {
	client Client
	name   string
}

func (p *clientProvider) Name() string { return p.name }

func (p *clientProvider) SuggestRepos(ctx context.Context, limit int) ([]Repo, error) {
	if cached, ok := freshCache(limit); ok {
		return cached, nil
	}
	repos, err := p.fetchAllRanked(ctx)
	if err != nil {
		return nil, err
	}

	_ = SaveCache(repos)
	return applyLimit(repos, limit), nil
}

func freshCache(limit int) ([]Repo, bool) {
	cached, age, err := LoadCache()
	if err != nil || len(cached) == 0 || age >= cacheTTL {
		return nil, false
	}
	return applyLimit(cached, limit), true
}

func (p *clientProvider) fetchAllRanked(ctx context.Context) ([]Repo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	username, err := p.client.CurrentUser()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repos, err := p.client.FetchRepos()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}

	activity, _ := p.client.FetchActivity(username)
	for i := range repos {
		repos[i].Activity = activity[repos[i].FullName]
	}
	sortByActivityThenPushed(repos)
	return repos, nil
}

func sortByActivityThenPushed(repos []Repo) {
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Activity != repos[j].Activity {
			return repos[i].Activity > repos[j].Activity
		}
		return repos[i].PushedAt.After(repos[j].PushedAt)
	})
}

func applyLimit(repos []Repo, limit int) []Repo {
	if limit > 0 && len(repos) > limit {
		out := make([]Repo, limit)
		copy(out, repos[:limit])
		return out
	}
	return repos
}

type noopProvider struct{}

func (noopProvider) Name() string { return "noop" }
func (noopProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotAuthed
}

func ResolveClient() (Client, error) {
	token, err := LoadToken()
	if err == nil && token.AccessToken != "" {
		return NewHTTPClient(token.AccessToken), nil
	}
	return nil, fmt.Errorf("no GitHub authentication found — run `ws auth login`")
}
