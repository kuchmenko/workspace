package add

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/github"
)

// fakeProvider is a minimal github.Provider for GitHubSource tests.
type fakeProvider struct {
	repos     []github.Repo
	err       error
	lastLimit int
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) SuggestRepos(ctx context.Context, limit int) ([]github.Repo, error) {
	f.lastLimit = limit
	return f.repos, f.err
}

func TestGitHubSource_ConvertsReposToSuggestions(t *testing.T) {
	now := time.Now()
	provider := &fakeProvider{
		repos: []github.Repo{
			{Name: "alpha", FullName: "me/alpha", Owner: "me", SSHURL: "git@github.com:me/alpha.git", Activity: 50, PushedAt: now},
			{Name: "infra", FullName: "myorg/infra", Owner: "myorg", SSHURL: "git@github.com:myorg/infra.git", Activity: 10, PushedAt: now.Add(-time.Hour)},
		},
	}

	src := &GitHubSource{Provider: provider}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}

	first := got[0]
	if first.Name != "alpha" || first.RemoteURL != "git@github.com:me/alpha.git" {
		t.Errorf("alpha: %+v", first)
	}
	if first.GhActivity != 50 {
		t.Errorf("Activity not propagated: %d", first.GhActivity)
	}
	if first.PushedAt.IsZero() {
		t.Error("PushedAt not propagated")
	}
	if !hasSource(first.Sources, SourceGitHub) {
		t.Errorf("Sources missing GitHub: %v", first.Sources)
	}
	if first.InferredGrp != "me" {
		t.Errorf("InferredGrp = %q, want me", first.InferredGrp)
	}

	// Org repo gets owner-as-group.
	second := got[1]
	if second.InferredGrp != "myorg" {
		t.Errorf("org InferredGrp = %q, want myorg", second.InferredGrp)
	}
}

func TestGitHubSource_PassesLimit(t *testing.T) {
	provider := &fakeProvider{}
	src := &GitHubSource{Provider: provider, Limit: 7}
	_, _ = src.FetchSuggestions(context.Background())
	if provider.lastLimit != 7 {
		t.Errorf("Limit not passed: got %d", provider.lastLimit)
	}
}

func TestGitHubSource_DefaultLimit(t *testing.T) {
	provider := &fakeProvider{}
	src := &GitHubSource{Provider: provider} // Limit zero
	_, _ = src.FetchSuggestions(context.Background())
	if provider.lastLimit != DefaultLimit {
		t.Errorf("default limit: got %d, want %d", provider.lastLimit, DefaultLimit)
	}
}

func TestGitHubSource_NilProviderErrors(t *testing.T) {
	src := &GitHubSource{}
	_, err := src.FetchSuggestions(context.Background())
	if err == nil {
		t.Error("expected error for nil Provider")
	}
}

func TestGitHubSource_PropagatesProviderError(t *testing.T) {
	provider := &fakeProvider{err: errors.New("api down")}
	src := &GitHubSource{Provider: provider}
	_, err := src.FetchSuggestions(context.Background())
	if err == nil {
		t.Error("expected provider error to propagate")
	}
}

func TestGitHubSource_NotAuthedSurfacesAsErr(t *testing.T) {
	provider := &fakeProvider{err: github.ErrNotAuthed}
	src := &GitHubSource{Provider: provider}
	_, err := src.FetchSuggestions(context.Background())
	if !errors.Is(err, github.ErrNotAuthed) {
		t.Errorf("want ErrNotAuthed wrapped, got %v", err)
	}
}

func TestGitHubSource_EmptyReposReturnsEmpty(t *testing.T) {
	provider := &fakeProvider{repos: nil}
	src := &GitHubSource{Provider: provider}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}
