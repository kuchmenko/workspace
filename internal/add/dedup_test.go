package add

import "testing"

func TestNormalizeRemoteURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// Canonical forms.
		{"ssh shorthand", "git@github.com:foo/bar.git", "github.com/foo/bar"},
		{"https with .git", "https://github.com/foo/bar.git", "github.com/foo/bar"},
		{"https without .git", "https://github.com/foo/bar", "github.com/foo/bar"},
		{"ssh full", "ssh://git@github.com/foo/bar.git", "github.com/foo/bar"},

		// Case insensitivity.
		{"mixed case host", "git@GitHub.com:Foo/Bar.git", "github.com/foo/bar"},

		// Trailing slash.
		{"trailing slash", "https://github.com/foo/bar/", "github.com/foo/bar"},

		// Other hosts.
		{"gitlab", "git@gitlab.com:group/sub/proj.git", "gitlab.com/group/sub/proj"},
		{"bitbucket https", "https://bitbucket.org/team/repo.git", "bitbucket.org/team/repo"},
		{"codeberg ssh", "git@codeberg.org:user/thing.git", "codeberg.org/user/thing"},

		// Edge cases.
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"malformed: passthrough", "not a url", "not a url"},
		{"https with port", "https://git.example.com:8080/foo/bar.git", "git.example.com:8080/foo/bar"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeRemoteURL(c.in)
			if got != c.want {
				t.Errorf("normalizeRemoteURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeRemoteURL_CrossFormMatch(t *testing.T) {
	// All five forms below describe the same logical repo. Normalization
	// must collapse them into one key — this is the critical property
	// for dedup to work across providers.
	variants := []string{
		"git@github.com:foo/bar.git",
		"git@github.com:foo/bar",
		"https://github.com/foo/bar.git",
		"https://github.com/foo/bar",
		"ssh://git@github.com/foo/bar.git",
	}

	first := normalizeRemoteURL(variants[0])
	for _, v := range variants[1:] {
		got := normalizeRemoteURL(v)
		if got != first {
			t.Errorf("%q normalized to %q; want %q (from %q)", v, got, first, variants[0])
		}
	}
}
