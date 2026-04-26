package github

import "context"

// GhAppProvider is a placeholder for the future GitHub App-backed
// Provider that will ship with its own installation flow, encrypted
// token storage, and rotation. Currently a pure stub: any
// SuggestRepos call returns ErrNotImplemented.
//
// The stub exists so the Provider interface shape stays stable
// before the App integration lands, and so callers can write
// `case *GhAppProvider:` switches that compile today.
//
// ResolveProvider does NOT wire this provider in — it picks
// httpClient → ghClient → noop. The future App integration will
// extend ResolveProvider to read ~/.config/ws/github-app.toml and
// return a real GhAppProvider when configured.
type GhAppProvider struct{}

// NewGhAppProviderStub constructs the placeholder. Named with an
// explicit "Stub" suffix so it's obvious at call sites that this
// does not actually talk to GitHub.
func NewGhAppProviderStub() *GhAppProvider { return &GhAppProvider{} }

func (*GhAppProvider) Name() string { return "gh-app" }

func (*GhAppProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotImplemented
}
