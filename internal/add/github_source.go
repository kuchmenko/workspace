package add

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/github"
)

type GitHubSource struct {
	Provider github.Provider

	Limit int

	KnownRemotes map[string]string
}

const DefaultLimit = 50

func (*GitHubSource) Name() string { return "github" }

func (s *GitHubSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	if s.Provider == nil {
		return nil, errors.New("GitHubSource: nil Provider")
	}
	limit := s.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	repos, err := s.Provider.SuggestRepos(ctx, limit)
	if err != nil {
		if errors.Is(err, github.ErrNotAuthed) {
			return nil, fmt.Errorf("github source: %w", err)
		}
		return nil, fmt.Errorf("github source: %w", err)
	}

	out := make([]Suggestion, 0, len(repos))
	for _, r := range repos {
		sug := Suggestion{
			Name:        r.Name,
			RemoteURL:   r.SSHURL,
			Sources:     []SourceKind{SourceGitHub},
			GhActivity:  r.Activity,
			PushedAt:    r.PushedAt,
			Description: r.Description,
			InferredGrp: r.Owner,
		}

		if s.KnownRemotes != nil {
			if p, ok := s.KnownRemotes[strings.ToLower(r.FullName)]; ok && p != "" {
				sug.RegisteredPath = p
			}
		}
		out = append(out, sug)
	}
	return out, nil
}
