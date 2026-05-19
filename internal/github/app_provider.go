package github

import "context"

type GhAppProvider struct{}

func NewGhAppProviderStub() *GhAppProvider { return &GhAppProvider{} }

func (*GhAppProvider) Name() string { return "gh-app" }

func (*GhAppProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotImplemented
}
