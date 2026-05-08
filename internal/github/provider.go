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

// Provider is the high-level interface the `ws add` TUI (and any other
// consumer that wants "top-N relevant repos for this user") talks to.
//
// It sits above the low-level Client interface — Client exposes the
// individual GitHub API calls (CurrentUser, FetchRepos, FetchActivity),
// and Provider composes them into a single "give me N suggestions"
// operation with sorting and limiting baked in.
//
// Today there's one real implementation (wraps either the OAuth
// httpClient or the gh-CLI client, whichever ResolveProvider picks)
// plus NoopProvider for the no-auth-configured case and GhAppProvider
// as a placeholder for the future GitHub App integration.
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

// ErrNotImplemented is returned by GhAppProvider until the GitHub
// App integration lands. The stub exists now so callers can code
// against the interface without conditional compilation.
var ErrNotImplemented = errors.New("not implemented (GitHub App)")

// ResolveProvider picks the best available Provider for this environment.
//
// Resolution order:
//  1. ws OAuth token present → clientProvider wrapping the HTTP client
//     (matches current github.ResolveClient behavior — keeps the
//     `ws auth login` flow as primary).
//  2. `gh auth status` succeeds → clientProvider wrapping the gh CLI
//     client (gh CLI becomes a real fallback, no longer orphan code).
//  3. Neither → NoopProvider. Callers see ErrNotAuthed on SuggestRepos
//     and render a "run `ws auth login` / `gh auth login`" hint.
//
// The OAuth path is health-probed before being returned: ws OAuth tokens
// from the device-flow path are user-to-server (`ghu_*`) with an 8-hour
// lifetime, and we don't refresh them. A stale token would silently 401
// every API call. The probe is one HEAD-style request to /user with a
// 2-second budget; if it fails (401, network, timeout), we fall through
// to gh CLI rather than wedge the TUI on a dead session.
//
// Does NOT read ~/.config/ws/github-app.toml. That reader lands with the
// future GitHub App integration so an empty/malformed token file cannot
// silently knock out httpClient/ghClient suggestions.
func ResolveProvider() Provider {
	// 1. ws OAuth token (preferred when valid).
	if token, err := loadOAuthToken(); err == nil && token != "" {
		client := NewHTTPClient(token)
		if oauthProbe(client) {
			return &clientProvider{client: client, name: "http-oauth"}
		}
		// Stale or rejected — silently fall through.
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

// oauthProbeNetwork performs a quick /user round-trip to validate the
// token before we hand the provider to the rest of the gather pipeline.
// Avoids the common "expired ghu_* token" trap where the user ran
// `ws auth login` weeks ago and the device-flow user-to-server token
// has long since lapsed.
//
// Returns true on a successful CurrentUser() call. Any error — 401,
// network, timeout — returns false, prompting the resolver to try the
// next path.
//
// 2s budget is generous enough for a transcontinental round-trip and
// strict enough that an unreachable api.github.com doesn't block the
// TUI cold-start.
//
// Swappable via the package-level `oauthProbe` variable for tests.
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

// loadOAuthToken, ghAuthStatus, and oauthProbe are package-level
// variables so tests can swap the environment probes without touching
// real auth state. Production defaults below.
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

	// oauthProbe validates a freshly-built httpClient by hitting /user
	// once. Swappable for tests that don't want to touch the network.
	oauthProbe = oauthProbeNetwork
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
	if cached, ok := freshCache(limit); ok {
		return cached, nil
	}
	repos, err := p.fetchAllRanked(ctx)
	if err != nil {
		return nil, err
	}
	// Persist the full result (pre-limit) so future requests for any
	// cap can be served from cache. Save errors are non-fatal — log
	// would be nice but the package has no logger; silent fallback
	// matches the rest of the cache layer.
	_ = SaveCache(repos)
	return applyLimit(repos, limit), nil
}

// freshCache returns the most-recent cached suggestion list when one
// exists and is still inside cacheTTL. Cache misses, expired entries,
// and read errors all return ok=false; callers fall through to a
// live fetch.
//
// Cache hit short-circuit serves repeated `ws add` invocations on
// large accounts (paginated GitHub fetches take 5-10s) without
// network roundtrips.
func freshCache(limit int) ([]Repo, bool) {
	cached, age, err := LoadCache()
	if err != nil || len(cached) == 0 || age >= cacheTTL {
		return nil, false
	}
	return applyLimit(cached, limit), true
}

// fetchAllRanked performs the live live-fetch + activity-merge +
// activity-sort sequence. The two ctx.Err() checks bracket the
// CurrentUser and FetchRepos calls — Client methods predate ctx
// plumbing, so those are the natural cancellation boundaries.
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
	// Activity fetch is best-effort — sort falls back to PushedAt
	// alone when it fails. Matches the legacy FetchAll behavior.
	activity, _ := p.client.FetchActivity(username)
	for i := range repos {
		repos[i].Activity = activity[repos[i].FullName]
	}
	sortByActivityThenPushed(repos)
	return repos, nil
}

// sortByActivityThenPushed orders repos by Activity descending,
// breaking ties by PushedAt descending. The two-key compare lives
// in one place so both the live and (in tests) cache-build paths
// agree on ordering.
func sortByActivityThenPushed(repos []Repo) {
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Activity != repos[j].Activity {
			return repos[i].Activity > repos[j].Activity
		}
		return repos[i].PushedAt.After(repos[j].PushedAt)
	})
}

// applyLimit caps the returned slice at `limit` entries when limit > 0.
// Used at both cache-read and live-fetch return paths so the public
// surface honors the cap regardless of the source.
func applyLimit(repos []Repo, limit int) []Repo {
	if limit > 0 && len(repos) > limit {
		// Defensive copy: callers may not expect the slice to alias
		// the cache's underlying array.
		out := make([]Repo, limit)
		copy(out, repos[:limit])
		return out
	}
	return repos
}

// noopProvider is the terminal "GitHub unavailable" fallback. Name is
// "noop" so callers can test for it without importing sentinel values.
type noopProvider struct{}

func (noopProvider) Name() string { return "noop" }
func (noopProvider) SuggestRepos(_ context.Context, _ int) ([]Repo, error) {
	return nil, ErrNotAuthed
}
