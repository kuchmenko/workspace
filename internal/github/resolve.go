package github

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/auth"
)

// ResolveClient returns a GitHub API client backed by the ws OAuth
// token. The ws CLI has its own GitHub OAuth App (see
// internal/auth/device_flow.go) and that is the single source of
// truth for authentication — there is no fallback to `gh` CLI. If
// you need to (re)authenticate, run `ws auth login`.
func ResolveClient() (Client, error) {
	token, err := auth.LoadToken()
	if err == nil && token.AccessToken != "" {
		return NewHTTPClient(token.AccessToken), nil
	}
	return nil, fmt.Errorf("no GitHub authentication found — run `ws auth login`")
}
