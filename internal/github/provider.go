package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/auth"
)

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
		token, err := auth.LoadToken()
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
