package github

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClient is a test double implementing Client for unit tests of
// clientProvider. Returns canned data plus optional errors so we can
// exercise success, API failure, and activity-best-effort paths.
type fakeClient struct {
	user       string
	userErr    error
	repos      []Repo
	reposErr   error
	activity   map[string]int
	actErr     error
	activityOk bool
}

func (f *fakeClient) CurrentUser() (string, error)     { return f.user, f.userErr }
func (f *fakeClient) FetchRepos() ([]Repo, error)      { return f.repos, f.reposErr }
func (f *fakeClient) FetchActivity(string) (map[string]int, error) {
	if !f.activityOk {
		return nil, f.actErr
	}
	return f.activity, nil
}

// swapResolveEnv swaps the package-level resolver probes for the test
// and returns a restore function. Tests deferring this stay hermetic.
//
// probeOk is true to make oauthProbe always succeed (i.e. token is
// valid), false to make it always fail (i.e. token is stale/rejected).
// nil → keep the production network probe (which will likely fail for
// the bogus tokens tests use; pass an explicit value to be safe).
func swapResolveEnv(loadFn func() (string, error), ghFn func() bool, probeOk *bool) func() {
	origLoad := loadOAuthToken
	origGh := ghAuthStatus
	origProbe := oauthProbe

	loadOAuthToken = loadFn
	ghAuthStatus = ghFn
	if probeOk != nil {
		v := *probeOk
		oauthProbe = func(_ Client) bool { return v }
	}
	return func() {
		loadOAuthToken = origLoad
		ghAuthStatus = origGh
		oauthProbe = origProbe
	}
}

func boolPtr(b bool) *bool { return &b }

func TestResolveProvider_PrefersOAuth(t *testing.T) {
	restore := swapResolveEnv(
		func() (string, error) { return "ws-oauth-token", nil },
		func() bool { return true }, // gh auth also ok — but OAuth wins
		boolPtr(true),               // probe ok → use OAuth
	)
	defer restore()

	p := ResolveProvider()
	if p.Name() != "http-oauth" {
		t.Errorf("want http-oauth, got %s", p.Name())
	}
}

func TestResolveProvider_FallsBackToGhCLI(t *testing.T) {
	restore := swapResolveEnv(
		func() (string, error) { return "", errors.New("no token") },
		func() bool { return true },
		boolPtr(true), // probe value irrelevant when no token
	)
	defer restore()

	p := ResolveProvider()
	if p.Name() != "gh-cli" {
		t.Errorf("want gh-cli, got %s", p.Name())
	}
}

func TestResolveProvider_FallsBackToGhCLIWhenProbeRejects(t *testing.T) {
	// Token loads fine but probe says 401 — must fall through to gh-cli.
	// This is the production scenario where ws OAuth ghu_* expired.
	restore := swapResolveEnv(
		func() (string, error) { return "stale-ghu-token", nil },
		func() bool { return true },
		boolPtr(false), // probe rejects → don't use OAuth
	)
	defer restore()

	if got := ResolveProvider().Name(); got != "gh-cli" {
		t.Errorf("want gh-cli (probe rejected OAuth), got %s", got)
	}
}

func TestResolveProvider_NoopWhenNothingConfigured(t *testing.T) {
	restore := swapResolveEnv(
		func() (string, error) { return "", errors.New("no token") },
		func() bool { return false },
		boolPtr(false),
	)
	defer restore()

	p := ResolveProvider()
	if p.Name() != "noop" {
		t.Errorf("want noop, got %s", p.Name())
	}
}

func TestResolveProvider_NoopWhenProbeRejectsAndNoGhCLI(t *testing.T) {
	// OAuth token present but stale, no gh CLI auth → noop.
	restore := swapResolveEnv(
		func() (string, error) { return "stale", nil },
		func() bool { return false },
		boolPtr(false),
	)
	defer restore()

	if got := ResolveProvider().Name(); got != "noop" {
		t.Errorf("want noop, got %s", got)
	}
}

func TestResolveProvider_IgnoresEmptyToken(t *testing.T) {
	// Token loaded OK but empty string — should fall through to gh-cli.
	restore := swapResolveEnv(
		func() (string, error) { return "", nil },
		func() bool { return true },
		boolPtr(true),
	)
	defer restore()

	if got := ResolveProvider().Name(); got != "gh-cli" {
		t.Errorf("want gh-cli, got %s", got)
	}
}

func TestNoopProvider_ReturnsErrNotAuthed(t *testing.T) {
	p := noopProvider{}
	_, err := p.SuggestRepos(context.Background(), 10)
	if !errors.Is(err, ErrNotAuthed) {
		t.Errorf("want ErrNotAuthed, got %v", err)
	}
}

func TestGhAppProviderStub_ReturnsErrNotImplemented(t *testing.T) {
	p := NewGhAppProviderStub()
	_, err := p.SuggestRepos(context.Background(), 10)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("want ErrNotImplemented, got %v", err)
	}
	if p.Name() != "gh-app" {
		t.Errorf("want gh-app, got %s", p.Name())
	}
}

func TestClientProvider_SortByActivityThenPushedAt(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-24 * time.Hour)
	much_earlier := now.Add(-72 * time.Hour)

	fc := &fakeClient{
		user: "me",
		repos: []Repo{
			{Name: "a", FullName: "me/a", PushedAt: much_earlier},
			{Name: "b", FullName: "me/b", PushedAt: now},
			{Name: "c", FullName: "me/c", PushedAt: earlier},
		},
		activity:   map[string]int{"me/a": 50, "me/b": 10, "me/c": 10},
		activityOk: true,
	}
	p := &clientProvider{client: fc, name: "fake"}

	got, err := p.SuggestRepos(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	// Expected order: a (activity 50) > c (activity 10, pushed earlier than b) — wait,
	// c pushed earlier than b means b.After(c) so when activity ties, b should come first.
	// Expected: a, b, c.
	wantOrder := []string{"a", "b", "c"}
	for i, w := range wantOrder {
		if got[i].Name != w {
			t.Errorf("pos %d: want %s, got %s (full: %v)", i, w, got[i].Name, names(got))
		}
	}
}

func TestClientProvider_LimitTrims(t *testing.T) {
	fc := &fakeClient{
		user:       "me",
		repos:      []Repo{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}},
		activityOk: true,
		activity:   map[string]int{},
	}
	p := &clientProvider{client: fc, name: "fake"}

	got, err := p.SuggestRepos(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 repos, got %d", len(got))
	}
}

func TestClientProvider_LimitZeroReturnsAll(t *testing.T) {
	fc := &fakeClient{
		user:       "me",
		repos:      []Repo{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		activityOk: true,
		activity:   map[string]int{},
	}
	p := &clientProvider{client: fc, name: "fake"}

	got, _ := p.SuggestRepos(context.Background(), 0)
	if len(got) != 3 {
		t.Errorf("want all 3, got %d", len(got))
	}
}

func TestClientProvider_ActivityErrorStillReturnsRepos(t *testing.T) {
	// Activity-fetch failure is non-fatal: we sort by PushedAt only.
	// Matches legacy FetchAll behavior that swallowed the error.
	now := time.Now()
	fc := &fakeClient{
		user: "me",
		repos: []Repo{
			{Name: "old", PushedAt: now.Add(-time.Hour)},
			{Name: "new", PushedAt: now},
		},
		activityOk: false,
		actErr:     errors.New("api down"),
	}
	p := &clientProvider{client: fc, name: "fake"}

	got, err := p.SuggestRepos(context.Background(), 0)
	if err != nil {
		t.Errorf("activity error should not be fatal: %v", err)
	}
	if got[0].Name != "new" {
		t.Errorf("expected 'new' first by PushedAt, got %+v", names(got))
	}
}

func TestClientProvider_UserErrorIsFatal(t *testing.T) {
	fc := &fakeClient{userErr: errors.New("401")}
	p := &clientProvider{client: fc, name: "fake"}

	_, err := p.SuggestRepos(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientProvider_ReposErrorIsFatal(t *testing.T) {
	fc := &fakeClient{user: "me", reposErr: errors.New("502")}
	p := &clientProvider{client: fc, name: "fake"}

	_, err := p.SuggestRepos(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientProvider_RespectsCancelledContext(t *testing.T) {
	fc := &fakeClient{user: "me"}
	p := &clientProvider{client: fc, name: "fake"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	_, err := p.SuggestRepos(ctx, 10)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func names(repos []Repo) []string {
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.Name
	}
	return out
}
