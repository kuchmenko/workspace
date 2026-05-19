// Package add is the shared core behind `ws add`. The same Run entry
// point handles three flavors:
//
//   - Standalone CLI: `ws add [url...]` and `ws add -` (stdin).
//   - Interactive TUI when invoked without URLs on a TTY.
//   - Embedded inside another bubbletea program (currently a stub
//     returning ErrEmbedNotSupported; reserved for the planned `ws
//     agent` add screen).
//
// The sidecar at ~/.local/state/ws/add/<sha>.toml is acquired before
// any workspace.toml mutation and released on every exit path (defer).
// The daemon reconciler's AnyActive sweep includes KindAdd, so a
// running `ws add` pauses the daemon for the affected workspace — no
// interleaving writes.
package add

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/clipboard"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/github"
	"github.com/kuchmenko/workspace/internal/tui"
)

var ErrEmbedNotSupported = errors.New("embedded mode not yet supported")

var ErrNoURLs = errors.New("no URLs provided; pass one or more git remote URLs")

func Run(ctx context.Context, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WsRoot == "" {
		return nil, errors.New("add.Run: empty WsRoot")
	}
	if opts.Workspace == nil {
		return nil, errors.New("add.Run: nil Workspace")
	}

	useTUI := false
	switch opts.Mode {
	case ModeTUI:
		useTUI = true
	case ModeEmbedded:
		return nil, ErrEmbedNotSupported
	case ModeAuto:
		if len(opts.URLs) == 0 {
			useTUI = true
		}

	case ModeHeadless:
		if len(opts.URLs) == 0 {
			return nil, ErrNoURLs
		}
	default:
		return nil, fmt.Errorf("add.Run: unknown mode %d", opts.Mode)
	}

	if _, err := acquireSidecar(opts.WsRoot, opts.Mode, opts.URLs); err != nil {
		return nil, err
	}
	defer releaseSidecar(opts.WsRoot)

	if useTUI {
		return runTUI(ctx, opts)
	}
	return runHeadless(ctx, opts)
}

func runTUI(ctx context.Context, opts Options) (*Result, error) {
	sources := buildSources(opts)

	model := NewAddModel(AddModelOptions{
		WsRoot:        opts.WsRoot,
		Workspace:     opts.Workspace,
		Save:          resolveSaveFn(opts),
		Sources:       sources,
		GatherTimeout: 10 * time.Second,
		Standalone:    true,
	})

	prog := tui.NewProgram(
		model,
		tui.WithAltScreen(),
		tui.WithContext(ctx),
	)

	finalModel, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("add TUI: %w", err)
	}

	final, ok := finalModel.(AddModel)
	if !ok {
		return nil, fmt.Errorf("add TUI: unexpected final model type %T", finalModel)
	}
	return &Result{
		Added:   final.added,
		Skipped: final.skipped,
		Errors:  final.errors,
	}, nil
}

func buildSources(opts Options) []Source {
	gh := opts.GhProvider
	if gh == nil {
		gh = github.ResolveProvider()
	}

	return []Source{
		NewDiskSource(opts.WsRoot, opts.Workspace),
		&ClipboardSource{Reader: clipboard.DefaultReader},
		&GitHubSource{
			Provider:     gh,
			KnownRemotes: knownRemotesFromWorkspace(opts.Workspace),
		},
	}
}

func knownRemotesFromWorkspace(ws *config.Workspace) map[string]string {
	if ws == nil || len(ws.Projects) == 0 {
		return nil
	}
	out := make(map[string]string, len(ws.Projects))
	for _, p := range ws.Projects {
		if p.Remote == "" {
			continue
		}
		key := ownerRepoFromRemote(p.Remote)
		if key == "" {
			continue
		}
		out[key] = p.Path
	}
	return out
}

func ownerRepoFromRemote(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	if at := strings.Index(s, "@"); at >= 0 && !strings.Contains(s, "://") {
		rest := s[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			s = rest[colon+1:]
			return strings.ToLower(s)
		}
	}

	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if slash := strings.Index(s, "/"); slash >= 0 {
			s = s[slash+1:]
		}
	}

	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return strings.ToLower(parts[0] + "/" + parts[1])
	}
	return ""
}

func resolveSaveFn(opts Options) func(*config.Workspace) error {
	if opts.Save != nil {
		return opts.Save
	}
	return func(ws *config.Workspace) error {
		return config.Save(opts.WsRoot, ws)
	}
}

func runHeadless(ctx context.Context, opts Options) (*Result, error) {
	res := &Result{}
	for _, url := range opts.URLs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, err)
			return res, nil
		}

		perURL := opts
		regRes, err := Register(perURL, url)
		if err != nil {
			if errors.Is(err, ErrAlreadyRegistered) {
				res.Skipped = append(res.Skipped, SkipReason{URL: url, Reason: err.Error()})
				continue
			}
			res.Errors = append(res.Errors, fmt.Errorf("%s: %w", url, err))
			continue
		}
		res.Added = append(res.Added, regRes.Project)
	}
	return res, nil
}
