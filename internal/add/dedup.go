package add

import (
	"net/url"
	"strings"
)

// normalizeRemoteURL collapses the many spellings of the same git remote
// into a single comparable key. Used by the suggestion-dedup path:
// when the disk source reports `git@github.com:foo/bar.git` and the
// GitHub source reports `https://github.com/foo/bar`, those are the
// same repo and should produce one Suggestion with Sources=[disk, gh].
//
// Normalized form: "<host><lowered>/<owner><lowered>/<repo><lowered>"
// with the trailing .git stripped and any leading scheme/user dropped.
// Lower-casing is safe here because GitHub, GitLab, and Bitbucket all
// treat owner/repo as case-insensitive; self-hosted forges that care
// about case should still normalize to one canonical form anyway.
//
// The function is designed to be forgiving: inputs it cannot parse
// (empty string, not a URL, weird scheme) pass through unchanged so
// the suggestion code can still de-duplicate on exact string equality
// in the worst case.
func normalizeRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// SSH shorthand: git@host:path → translate to ssh://git@host/path
	// so url.Parse understands it.
	if idx := strings.Index(s, "@"); idx > 0 && !strings.Contains(s, "://") {
		rest := s[idx+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			host := rest[:colon]
			path := rest[colon+1:]
			s = "ssh://" + s[:idx+1] + host + "/" + strings.TrimPrefix(path, "/")
		}
	}

	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Path == "" {
		// Couldn't parse — return the original trimmed string. Dedup
		// still works for exact-duplicate strings, just not across
		// ssh/https variants.
		return s
	}

	host := strings.ToLower(u.Host)
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.ToLower(path)

	if path == "" {
		return host
	}
	return host + "/" + path
}
