package github

import "context"

// GhAppProvider is the Track C placeholder — a GitHub App-backed
// Provider that will ship with its own installation flow, encrypted
// token storage, and rotation. In Phase 1 it is a pure stub: any
// SuggestRepos call returns ErrNotImplemented.
//
// The stub exists so the Provider interface shape is locked down
// before Track C starts, and so callers can write `case *GhAppProvider:`
// switches that compile today.
//
// Phase 1 resolve logic does NOT wire this provider in. ResolveProvider
// returns httpClient → ghClient → noop, never GhAppProvider. Track C
// adds the reader for ~/.config/ws/github-app.toml and the install flow.
type GhAppProvider struct{}

// NewGhAppProviderStub constructs the Phase 1 stub. Named with an
// explicit "Stub" suffix so it's obvious at call sites that this does
// not actually talk to GitHub.
func NewGhAppProviderStub() *GhAppProvider { return &GhAppProvider{} }

func (*GhAppProvider) Name() string { return "gh-app" }

func (*GhAppProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotImplemented
}
