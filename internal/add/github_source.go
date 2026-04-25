package add

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/github"
)

// GitHubSource wraps github.Provider into the Source contract used by
// the gather pipeline. It converts each github.Repo into a Suggestion
// with SourceGitHub and the activity/PushedAt fields filled in.
//
// The number of suggestions returned is capped by Limit (default 50)
// to keep the TUI readable. Callers wanting "all repos" pass Limit=0.
type GitHubSource struct {
	// Provider is the github.Provider to query. Required.
	Provider github.Provider

	// Limit caps the number of suggestions per call. 0 → DefaultLimit.
	Limit int

	// KnownRemotes maps lowercased "owner/repo" to the workspace.toml
	// project path. Suggestions whose FullName hits this map get
	// RegisteredPath set so the TUI can highlight already-cloned
	// repositories. Empty/nil → no highlights, all repos surface as
	// fresh suggestions.
	KnownRemotes map[string]string
}

// DefaultLimit is the suggestion cap when GitHubSource.Limit is 0.
// 50 is enough for the sole user's account today; revisit if 10x-scale
// users complain.
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
		// Treat ErrNotAuthed as "source unavailable" — silent, like
		// a missing clipboard tool. Gather records the error in
		// PerSource so the TUI can show a "run gh auth login" chip.
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
			InferredGrp: r.Owner, // GitHub owner = inferred group (org or self)
		}
		// Cross-reference workspace.toml: if a project with this
		// remote is already registered, surface its path so the TUI
		// can highlight the suggestion as "already cloned at X".
		if s.KnownRemotes != nil {
			if p, ok := s.KnownRemotes[strings.ToLower(r.FullName)]; ok && p != "" {
				sug.RegisteredPath = p
			}
		}
		out = append(out, sug)
	}
	return out, nil
}
