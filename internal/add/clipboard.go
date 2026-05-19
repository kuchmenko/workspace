package add

import (
	"context"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/kuchmenko/workspace/internal/clipboard"
	"github.com/kuchmenko/workspace/internal/git"
)

type ClipboardSource struct {
	Reader clipboard.Reader

	AllowedHostsExtra []string
}

func (*ClipboardSource) Name() string { return "clipboard" }

func (s *ClipboardSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	r := s.Reader
	if r == nil {
		r = clipboard.DefaultReader
	}

	raw, err := r.Read(ctx)
	if err != nil {
		if errors.Is(err, clipboard.ErrUnavailable) {
			return nil, nil
		}
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !looksLikeGitURL(raw, s.allowedHosts()) {
		return nil, nil
	}

	name := git.ParseRepoName(raw)
	return []Suggestion{{
		Name:      name,
		RemoteURL: raw,
		Sources:   []SourceKind{SourceClipboard},
	}}, nil
}

func (s *ClipboardSource) allowedHosts() map[string]bool {
	hosts := map[string]bool{
		"github.com":    true,
		"gitlab.com":    true,
		"bitbucket.org": true,
		"codeberg.org":  true,
	}
	if env := os.Getenv("WS_GIT_HOSTS"); env != "" {
		for _, h := range strings.Split(env, ":") {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				hosts[h] = true
			}
		}
	}
	for _, h := range s.AllowedHostsExtra {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

var shorthandRegex = regexp.MustCompile(
	`^[a-zA-Z0-9._-]+@([a-zA-Z0-9.-]+):([a-zA-Z0-9._/-]+?)(?:\.git)?/?$`,
)

var ownerRepoPath = regexp.MustCompile(
	`^/[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+/?$`,
)

func looksLikeGitURL(s string, allowedHosts map[string]bool) bool {
	s = strings.TrimSpace(s)

	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}

	if m := shorthandRegex.FindStringSubmatch(s); m != nil {
		host := strings.ToLower(m[1])

		if allowedHosts[host] {
			return true
		}

		return true
	}

	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http", "ssh", "git":

	default:
		return false
	}
	if u.Host == "" {
		return false
	}

	host := strings.ToLower(u.Host)

	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}

	pathTrimmed := strings.TrimSuffix(u.Path, "/")

	if strings.HasSuffix(pathTrimmed, ".git") {
		return true
	}

	if allowedHosts[host] {
		if ownerRepoPath.MatchString(pathTrimmed+"/") || ownerRepoPath.MatchString(pathTrimmed) {
			return true
		}
		return false
	}

	if ownerRepoPath.MatchString(pathTrimmed) || ownerRepoPath.MatchString(pathTrimmed+"/") {
		return true
	}
	return false
}
