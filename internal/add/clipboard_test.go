package add

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

type fakeClipboardReader struct {
	val string
	err error
}

func (f fakeClipboardReader) Read(_ context.Context) (string, error) {
	return f.val, f.err
}

func TestDetectClipboard_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("detectClipboard returns ErrClipboardUnavailable only on unsupported platforms; this GOOS is supported")
	}
	_, _, err := detectClipboard()
	if !errors.Is(err, ErrClipboardUnavailable) {
		t.Errorf("want ErrClipboardUnavailable, got %v", err)
	}
}

func TestSystemClipboardReader_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := DefaultClipboardReader.Read(ctx)
	if err == nil {
		t.Fatal("expected error from canceled context or missing tool")
	}
	if !errors.Is(err, ErrClipboardUnavailable) && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSystemClipboardReader_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)

	_, err := DefaultClipboardReader.Read(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClipboardReaderInterface_CanSwapDefault(t *testing.T) {
	orig := DefaultClipboardReader
	t.Cleanup(func() { DefaultClipboardReader = orig })

	DefaultClipboardReader = fakeClipboardReader{val: "git@github.com:foo/bar.git"}
	got, err := DefaultClipboardReader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "git@github.com:foo/bar.git" {
		t.Errorf("got %q", got)
	}
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
		{"ssh shorthand", "git@github.com:foo/bar.git", true},
		{"ssh shorthand no .git", "git@github.com:foo/bar", true},
		{"ssh shorthand unknown host", "git@self-hosted.example:foo/bar", true},

		{"https with .git", "https://github.com/foo/bar.git", true},
		{"http with .git", "http://example.com/foo/bar.git", true},
		{"ssh:// scheme", "ssh://git@github.com/foo/bar.git", true},
		{"git:// scheme", "git://example.com/foo/bar.git", true},

		{"github plain", "https://github.com/foo/bar", true},
		{"gitlab plain", "https://gitlab.com/foo/bar", true},
		{"bitbucket plain", "https://bitbucket.org/foo/bar", true},

		{"unknown host owner/repo", "https://gittea.example.com/foo/bar", true},

		{"github root", "https://github.com/", false},
		{"github single segment", "https://github.com/foo", false},
		{"github deep path", "https://github.com/foo/bar/baz", false},
		{"web URL", "https://example.com", false},
		{"web URL with path", "https://news.ycombinator.com/item?id=1", false},

		{"empty", "", false},
		{"whitespace", "   ", false},
		{"newline embedded", "git@github.com:foo/bar.git\nextra", false},

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
		Reader: fakeClipboardReader{err: ErrClipboardUnavailable},
	}
	got, err := src.FetchSuggestions(context.Background())
	if err != nil {
		t.Errorf("ErrClipboardUnavailable should be silent, got %v", err)
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
		t.Error("expected error to propagate (non-ErrClipboardUnavailable)")
	}
}

func TestClipboardSource_AllowedHostsExtra(t *testing.T) {
	src := &ClipboardSource{
		Reader: fakeClipboardReader{val: "https://myforge.example/repo-only"},
	}
	got, _ := src.FetchSuggestions(context.Background())
	if len(got) != 0 {
		t.Errorf("expected reject without whitelist, got %v", got)
	}

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
