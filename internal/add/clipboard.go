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

// ClipboardSource reads the system clipboard via internal/clipboard
// and surfaces its contents as a Suggestion when (and only when) the
// content looks like a git remote URL.
//
// The "looks like a git URL" test is intentionally tight per the issue
// review (issue #20 v2 Minor: clipboard regex boundary). A bare
// "https://example.com" must NOT surface as a chip — that's a generic
// web URL, not a repo. Acceptance is the conjunction of:
//
//   - scheme is one of https, http, ssh, git (plus the git@host:owner
//     shorthand), AND
//   - one of:
//     - path ends in `.git`
//     - host is in the known-forge whitelist (github / gitlab /
//       bitbucket / codeberg + $WS_GIT_HOSTS, colon-separated)
//     - path matches `^/<owner>/<repo>$` shape (single segment owner +
//       repo, no deeper path)
//
// Anything else returns no suggestion (empty slice, nil error). The
// source never fails fatally — clipboard tool missing is silent
// (ErrUnavailable swallowed), regex miss is silent.
type ClipboardSource struct {
	// Reader overrides the default clipboard reader. nil → use
	// internal/clipboard.DefaultReader. Tests inject fakes.
	Reader clipboard.Reader

	// AllowedHostsExtra extends the built-in known-forge whitelist with
	// per-call additions. Useful when callers want to honor an env
	// override without touching environment globals.
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
		// Tool missing is a no-op for this source. Other errors (ctx
		// cancel/timeout) propagate so Gather can record the cause in
		// PerSource diagnostics.
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

// allowedHosts merges the built-in forge whitelist with the env override
// $WS_GIT_HOSTS (colon-separated) and any AllowedHostsExtra. Lower-cased
// for host comparison.
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

// shorthandRegex matches the SCP-style `git@host:owner/repo[.git]` form
// that ssh shorthand uses. Captured groups: 1=host, 2=owner, 3=repo.
var shorthandRegex = regexp.MustCompile(
	`^[a-zA-Z0-9._-]+@([a-zA-Z0-9.-]+):([a-zA-Z0-9._/-]+?)(?:\.git)?/?$`,
)

// ownerRepoPath matches `/<owner>/<repo>[/]` with single-segment owner
// and repo. The TrimSuffix on `.git` is done before the regex so we
// don't have to handle it inside.
var ownerRepoPath = regexp.MustCompile(
	`^/[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+/?$`,
)

// looksLikeGitURL is the tightened content filter. See the doc comment
// on ClipboardSource for the policy.
func looksLikeGitURL(s string, allowedHosts map[string]bool) bool {
	s = strings.TrimSpace(s)

	// Reject inputs with whitespace or newlines anywhere — the clipboard
	// is allowed to contain multi-line text, but a multi-line "URL" is
	// not a single URL.
	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}

	// SCP-style `git@host:owner/repo`.
	if m := shorthandRegex.FindStringSubmatch(s); m != nil {
		host := strings.ToLower(m[1])
		// Always trust shorthand on a known host; otherwise require the
		// path to also look like owner/repo (which the regex enforces
		// by structure already).
		if allowedHosts[host] {
			return true
		}
		// Even on unknown hosts, accept shorthand — it is unambiguously
		// a git form and not a generic web URL. (The web does not have
		// `git@host:path` URIs.)
		return true
	}

	// scheme://... forms.
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http", "ssh", "git":
		// allowed schemes
	default:
		return false
	}
	if u.Host == "" {
		return false
	}

	host := strings.ToLower(u.Host)
	// Strip port for whitelist matching: `git.example.com:8080` → `git.example.com`.
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}

	pathTrimmed := strings.TrimSuffix(u.Path, "/")

	// 1. .git suffix is the unambiguous marker.
	if strings.HasSuffix(pathTrimmed, ".git") {
		return true
	}
	// 2. Known forge whitelist.
	if allowedHosts[host] {
		// Belt + braces: even on a known forge, require something
		// resembling owner/repo. `https://github.com/` alone is the
		// forge front page, not a repo.
		if ownerRepoPath.MatchString(pathTrimmed + "/") || ownerRepoPath.MatchString(pathTrimmed) {
			return true
		}
		return false
	}
	// 3. owner/repo shape on an unknown host: accept (covers
	//    self-hosted Gitea/Forgejo via $WS_GIT_HOSTS, but also any
	//    generic git host the user happens to use).
	if ownerRepoPath.MatchString(pathTrimmed) || ownerRepoPath.MatchString(pathTrimmed+"/") {
		return true
	}
	return false
}
