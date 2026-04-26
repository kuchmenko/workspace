package add

import (
	"context"
	"errors"
	"testing"

	"github.com/kuchmenko/workspace/internal/clipboard"
)

// fakeClipboardReader returns canned content for ClipboardSource tests.
type fakeClipboardReader struct {
	val string
	err error
}

func (f fakeClipboardReader) Read(_ context.Context) (string, error) {
	return f.val, f.err
}

func TestLooksLikeGitURL(t *testing.T) {
	hosts := map[string]bool{
		"github.com":    true,
		"gitlab.com":    true,
		"bitbucket.org": true,
	}

	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Accepted: SCP shorthand.
		{"ssh shorthand", "git@github.com:foo/bar.git", true},
		{"ssh shorthand no .git", "git@github.com:foo/bar", true},
		{"ssh shorthand unknown host", "git@self-hosted.example:foo/bar", true},

		// Accepted: scheme + .git suffix.
		{"https with .git", "https://github.com/foo/bar.git", true},
		{"http with .git", "http://example.com/foo/bar.git", true},
		{"ssh:// scheme", "ssh://git@github.com/foo/bar.git", true},
		{"git:// scheme", "git://example.com/foo/bar.git", true},

		// Accepted: known forge host + owner/repo path.
		{"github plain", "https://github.com/foo/bar", true},
		{"gitlab plain", "https://gitlab.com/foo/bar", true},
		{"bitbucket plain", "https://bitbucket.org/foo/bar", true},

		// Accepted: unknown host but owner/repo shape.
		{"unknown host owner/repo", "https://gittea.example.com/foo/bar", true},

		// Rejected: forge front pages, deep paths, non-git URLs.
		{"github root", "https://github.com/", false},
		{"github single segment", "https://github.com/foo", false},
		{"github deep path", "https://github.com/foo/bar/baz", false},
		{"web URL", "https://example.com", false},
		{"web URL with path", "https://news.ycombinator.com/item?id=1", false},

		// Rejected: empty / whitespace / multi-line.
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"newline embedded", "git@github.com:foo/bar.git\nextra", false},

		// Rejected: scheme is unknown.
		{"file scheme", "file:///tmp/foo.git", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := looksLikeGitURL(c.in, hosts)
			if got != c.want {
				t.Errorf("looksLikeGitURL(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestClipboardSource_AcceptsGitURL(t *testing.T) {
	src := &ClipboardSource{
		Reader: fakeClipboardReader{val: "git@github.com:me/proj.git"},
	}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 suggestion, got %d", len(got))
	}
	s := got[0]
	if s.RemoteURL != "git@github.com:me/proj.git" {
		t.Errorf("RemoteURL = %q", s.RemoteURL)
	}
	if s.Name != "proj" {
		t.Errorf("Name = %q, want proj", s.Name)
	}
	if !hasSource(s.Sources, SourceClipboard) {
		t.Errorf("Sources missing Clipboard: %v", s.Sources)
	}
}

func TestClipboardSource_RejectsNonGitContent(t *testing.T) {
	cases := []string{
		"hello world",
		"https://news.ycombinator.com/item?id=1",
		"random text",
		"",
		"https://example.com",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			src := &ClipboardSource{Reader: fakeClipboardReader{val: in}}
			got, err := src.FetchSuggestions(context.Background())
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("expected no suggestion for %q, got %+v", in, got)
			}
		})
	}
}

func TestClipboardSource_TrimsWhitespace(t *testing.T) {
	src := &ClipboardSource{
		Reader: fakeClipboardReader{val: "  https://github.com/foo/bar.git  "},
	}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RemoteURL != "https://github.com/foo/bar.git" {
		t.Errorf("trim failed: %+v", got)
	}
}

func TestClipboardSource_UnavailableIsSilent(t *testing.T) {
	src := &ClipboardSource{
		Reader: fakeClipboardReader{err: clipboard.ErrUnavailable},
	}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Errorf("ErrUnavailable should be silent, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no suggestions, got %v", got)
	}
}

func TestClipboardSource_OtherErrorPropagates(t *testing.T) {
	src := &ClipboardSource{
		Reader: fakeClipboardReader{err: errors.New("fake")},
	}
	_, err := src.FetchSuggestions(context.Background())
	if err == nil {
		t.Error("expected error to propagate (non-ErrUnavailable)")
	}
}

func TestClipboardSource_AllowedHostsExtra(t *testing.T) {
	// "myforge.example" is not in the built-in whitelist. Plain URL
	// without .git or owner/repo shape rejected.
	src := &ClipboardSource{
		Reader: fakeClipboardReader{val: "https://myforge.example/repo-only"},
	}
	got, _ := src.FetchSuggestions(context.Background())
	if len(got) != 0 {
		t.Errorf("expected reject without whitelist, got %v", got)
	}

	// Test with known shape — owner/repo, single segment each.
	src.Reader = fakeClipboardReader{val: "https://myforge.example/team/api"}
	got, _ = src.FetchSuggestions(context.Background())
	if len(got) != 1 {
		t.Errorf("expected accept owner/repo on unknown host, got %d", len(got))
	}
}

func TestClipboardSource_EnvOverride(t *testing.T) {
	t.Setenv("WS_GIT_HOSTS", "myforge.example:other.host")
	src := &ClipboardSource{
		Reader: fakeClipboardReader{val: "https://myforge.example/team/repo"},
	}
	got, _ := src.FetchSuggestions(context.Background())
	if len(got) != 1 {
		t.Errorf("env override host: got %d, want 1", len(got))
	}
}
