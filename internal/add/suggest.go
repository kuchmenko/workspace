package add

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

type Source interface {
	FetchSuggestions(ctx context.Context) ([]Suggestion, error)

	Name() string
}

type SourceKind int

const (
	SourceClipboard SourceKind = iota
	SourceGitHub
	SourceManual
)

func (k SourceKind) String() string {
	switch k {
	case SourceClipboard:
		return "clip"
	case SourceGitHub:
		return "gh"
	case SourceManual:
		return "manual"
	default:
		return "?"
	}
}

type Suggestion struct {
	Name string

	RemoteURL string

	Sources []SourceKind

	RegisteredPath string

	GhActivity int

	PushedAt time.Time

	Description string

	InferredGrp string
}

type SourceOutcome struct {
	Name     string
	Count    int
	Duration time.Duration
	Err      error
}

const DefaultSourceTimeout = 3 * time.Second

func mergeSuggestions(buckets [][]Suggestion) []Suggestion {
	byKey := make(map[string]*Suggestion)
	for _, bucket := range buckets {
		for _, s := range bucket {
			key := normalizeRemoteURL(s.RemoteURL)
			if key == "" {
				key = "name:" + s.Name
			}
			cur, ok := byKey[key]
			if !ok {
				copy := s
				byKey[key] = &copy
				continue
			}

			cur.Sources = unionSources(cur.Sources, s.Sources)
			if cur.RegisteredPath == "" {
				cur.RegisteredPath = s.RegisteredPath
			}
			if cur.Description == "" {
				cur.Description = s.Description
			}
			if cur.GhActivity == 0 {
				cur.GhActivity = s.GhActivity
			}
			if cur.PushedAt.IsZero() {
				cur.PushedAt = s.PushedAt
			}
			if cur.Name == "" {
				cur.Name = s.Name
			}
			if cur.RemoteURL == "" {
				cur.RemoteURL = s.RemoteURL
			}
			if cur.InferredGrp == "" {
				cur.InferredGrp = s.InferredGrp
			}
		}
	}

	out := make([]Suggestion, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, *v)
	}
	return out
}

func unionSources(a, b []SourceKind) []SourceKind {
	out := append([]SourceKind{}, a...)
Outer:
	for _, kb := range b {
		for _, ka := range out {
			if ka == kb {
				continue Outer
			}
		}
		out = append(out, kb)
	}
	return out
}

func sortByRelevance(s []Suggestion) {
	sort.SliceStable(s, func(i, j int) bool {
		_, li, oi := groupKey(s[i])
		_, lj, oj := groupKey(s[j])
		if oi != oj {
			return oi < oj
		}
		if li != lj {
			return strings.ToLower(li) < strings.ToLower(lj)
		}

		if s[i].GhActivity != s[j].GhActivity {
			return s[i].GhActivity > s[j].GhActivity
		}
		if !s[i].PushedAt.Equal(s[j].PushedAt) {
			return s[i].PushedAt.After(s[j].PushedAt)
		}
		return s[i].Name < s[j].Name
	})
}

func hasSource(ss []SourceKind, k SourceKind) bool {
	for _, x := range ss {
		if x == k {
			return true
		}
	}
	return false
}

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

func emit(msg tui.Msg) tui.Cmd {
	return func() tui.Msg { return msg }
}

func parseRepoNameFromURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		return url[i+1:]
	}
	return url
}

func addPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func renderSourceChips(srcs []SourceKind) string {
	if len(srcs) == 0 {
		return ""
	}
	var parts []string
	for _, k := range srcs {
		parts = append(parts, addChip.Render("["+k.String()+"]"))
	}
	return strings.Join(parts, " ")
}

func shortURL(s Suggestion) string {
	if s.RemoteURL != "" {
		return s.RemoteURL
	}
	return ""
}

func renderSourceChipsLive(outcomes []SourceOutcome) string {
	var chips []string
	for _, o := range outcomes {
		var color string
		var label string
		switch {
		case o.Err != nil:
			color = "3"
			label = fmt.Sprintf("%s:err (%s)", o.Name, sourceErrHint(o.Err))
		case o.Count == 0:
			color = "8"
			label = fmt.Sprintf("%s:0", o.Name)
		default:
			color = "2"
			label = fmt.Sprintf("%s:%d", o.Name, o.Count)
		}
		chips = append(chips, tui.NewStyle().
			Foreground(tui.Color(color)).Render(label))
	}
	return strings.Join(chips, "  ")
}

func sourceErrHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case strings.Contains(msg, "ErrNotAuthed"), strings.Contains(msg, "not authed"):
		return "no auth"
	case strings.Contains(strings.ToLower(msg), "rate limit"),
		strings.Contains(msg, "API rate limit"):
		return "rate-limited"
	case strings.Contains(strings.ToLower(msg), "401"),
		strings.Contains(strings.ToLower(msg), "unauthorized"):
		return "401 expired?"
	case strings.Contains(msg, "Nothing is copied"),
		strings.Contains(msg, "No selection"):
		return "empty"
	}

	tail := msg
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		tail = strings.TrimSpace(msg[i+2:])
	}
	tail = strings.ReplaceAll(tail, "\n", " ")
	if len(tail) > 24 {
		tail = tail[:24]
	}
	return tail
}
