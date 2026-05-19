package add

import (
	"net/url"
	"strings"
)

func normalizeRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

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
