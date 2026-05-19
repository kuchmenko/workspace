package add

import (
	"context"
	"sort"
	"strings"
	"time"
)

type Source interface {
	FetchSuggestions(ctx context.Context) ([]Suggestion, error)

	Name() string
}

type SourceKind int

const (
	SourceDisk SourceKind = iota
	SourceClipboard
	SourceGitHub
	SourceManual
)

func (k SourceKind) String() string {
	switch k {
	case SourceDisk:
		return "disk"
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

	DiskPath string

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
			if cur.DiskPath == "" {
				cur.DiskPath = s.DiskPath
			}
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

		diskI := hasSource(s[i].Sources, SourceDisk)
		diskJ := hasSource(s[j].Sources, SourceDisk)
		if diskI != diskJ {
			return diskI
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
