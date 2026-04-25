package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"

	"github.com/kuchmenko/workspace/internal/auth"
)

// Provider is the high-level interface the `ws add` TUI (and any other
// consumer that wants "top-N relevant repos for this user") talks to.
//
// It sits above the low-level Client interface — Client exposes the
// individual GitHub API calls (CurrentUser, FetchRepos, FetchActivity),
// and Provider composes them into a single "give me N suggestions"
// operation with sorting and limiting baked in.
//
// Phase 1 ships one real implementation (wraps either the OAuth
// httpClient or the gh-CLI client, whichever ResolveProvider selects)
// plus NoopProvider (no auth available) and GhAppProvider (Track C
// stub).
type Provider interface {
	// SuggestRepos returns up to `limit` repos sorted by recent activity.
	// ctx is honored for cancellation; implementations should bail out
	// promptly on ctx.Done().
	SuggestRepos(ctx context.Context, limit int) ([]Repo, error)

	// Name returns a short machine-readable identifier of the underlying
	// backend — "http-oauth", "gh-cli", "gh-app", "noop". Used for
	// diagnostics ("GitHub suggestions via gh-cli") and for telemetry
	// that shouldn't leak through the abstraction.
	Name() string
}

// ErrNotAuthed is returned by NoopProvider.SuggestRepos when no GitHub
// authentication is configured. Callers should surface this to the user
// as "run `ws auth login` or `gh auth login`", not as a fatal error —
// the `ws add` TUI degrades gracefully when GitHub is unavailable.
var ErrNotAuthed = errors.New("no GitHub authentication configured")

// ErrNotImplemented is returned by GhAppProvider in Phase 1. The App
// provider lands with Track C; the stub exists now so callers can code
// against the interface without conditional compilation.
var ErrNotImplemented = errors.New("not implemented (GitHub App: Track C)")

// ResolveProvider picks the best available Provider for this environment.
//
// Resolution order (Phase 1):
//  1. ws OAuth token present → clientProvider wrapping the HTTP client
//     (matches current github.ResolveClient behavior — keeps the
//     `ws auth login` flow as primary).
//  2. `gh auth status` succeeds → clientProvider wrapping the gh CLI
//     client (gh CLI becomes a real fallback, no longer orphan code).
//  3. Neither → NoopProvider. Callers see ErrNotAuthed on SuggestRepos
//     and render a "run `ws auth login` / `gh auth login`" hint.
//
// Phase 1 does NOT read ~/.config/ws/github-app.toml. That file and its
// reader land with Track C together so an empty/malformed token file
// cannot silently knock out httpClient/ghClient suggestions.
func ResolveProvider() Provider {
	// 1. ws OAuth token (preferred).
	if token, err := loadOAuthToken(); err == nil && token != "" {
		return &clientProvider{
			client: NewHTTPClient(token),
			name:   "http-oauth",
		}
	}

	// 2. gh CLI fallback.
	if ghAuthStatus() {
		return &clientProvider{
			client: NewGHClient(),
			name:   "gh-cli",
		}
	}

	// 3. No auth available.
	return noopProvider{}
}

// loadOAuthToken and ghAuthStatus are package-level variables so tests
// can swap the environment probes without touching real auth state.
// Production defaults below.
var (
	loadOAuthToken = func() (string, error) {
		token, err := auth.LoadToken()
		if err != nil {
			return "", err
		}
		return token.AccessToken, nil
	}

	ghAuthStatus = func() bool {
		// `gh auth status` exits 0 when any host has a valid token.
		// We don't parse the output — exit code is the contract.
		cmd := exec.Command("gh", "auth", "status")
		return cmd.Run() == nil
	}
)

// clientProvider adapts any Client (httpClient or ghClient today; more
// in the future) into a Provider. The logic here is identical to what
// FetchAll does today — we moved it behind the interface so callers
// don't have to care which backend is in use.
type clientProvider struct {
	client Client
	name   string
}

func (p *clientProvider) Name() string { return p.name }

func (p *clientProvider) SuggestRepos(ctx context.Context, limit int) ([]Repo, error) {
	// Client methods predate ctx; we check cancellation at the two
	// natural boundaries (before and between calls). Full ctx-plumbing
	// into the Client interface is out of scope for Phase 1-B.
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

	// Activity fetch is best-effort — if it fails, we sort by PushedAt
	// only. Matches the legacy FetchAll behavior (which swallowed the
	// error and kept going).
	activity, _ := p.client.FetchActivity(username)
	for i := range repos {
		repos[i].Activity = activity[repos[i].FullName]
	}

	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Activity != repos[j].Activity {
			return repos[i].Activity > repos[j].Activity
		}
		return repos[i].PushedAt.After(repos[j].PushedAt)
	})

	if limit > 0 && len(repos) > limit {
		repos = repos[:limit]
	}
	return repos, nil
}

// noopProvider is the terminal "GitHub unavailable" fallback. Name is
// "noop" so callers can test for it without importing sentinel values.
type noopProvider struct{}

func (noopProvider) Name() string { return "noop" }
func (noopProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotAuthed
}
