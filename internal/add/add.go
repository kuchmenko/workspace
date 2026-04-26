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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/clipboard"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/github"
)

// ErrEmbedNotSupported is returned when a caller asks for ModeEmbedded.
// Reserved for a future `ws agent` add screen that will host AddModel
// inside its own bubbletea program; the interface is frozen now so
// the embedding contract is locked.
var ErrEmbedNotSupported = errors.New("embedded mode not yet supported")

// ErrNoURLs is returned by headless runs with Options.URLs empty.
// The TUI is the usual answer to "I didn't bring URLs"; headless by
// definition cannot consult the user.
var ErrNoURLs = errors.New("no URLs provided; pass one or more git remote URLs")

// Run is the single entry point for every `ws add` style operation.
// It owns the sidecar lifecycle and dispatches on Mode:
//
//	ModeAuto     with URLs    → headless
//	ModeAuto     without URLs → TUI
//	ModeHeadless with URLs    → headless
//	ModeHeadless without URLs → ErrNoURLs
//	ModeTUI                   → TUI
//	ModeEmbedded              → ErrEmbedNotSupported (reserved)
//
// Per-URL errors accumulate in Result.Errors and do NOT abort the
// loop; Run returns a non-nil error only when the whole operation
// fails to start (nil required fields, sidecar conflict, ctx cancel
// before the first register).
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
		// ModeAuto with URLs → headless (useTUI stays false).
	case ModeHeadless:
		if len(opts.URLs) == 0 {
			return nil, ErrNoURLs
		}
	default:
		return nil, fmt.Errorf("add.Run: unknown mode %d", opts.Mode)
	}

	// Sidecar acquire (blocks concurrent `ws add`, pauses the daemon).
	// We don't read the returned sidecar struct — the file's mere
	// presence with our pid is the contract; `defer releaseSidecar`
	// removes it on every exit.
	if _, err := acquireSidecar(opts.WsRoot, opts.Mode, opts.URLs); err != nil {
		return nil, err
	}
	defer releaseSidecar(opts.WsRoot)

	if useTUI {
		return runTUI(ctx, opts)
	}
	return runHeadless(ctx, opts)
}

// runTUI launches AddModel as a standalone tea.Program and returns
// the Result accumulated by the model when it reaches its done state.
//
// The sources used by the gather pass come from buildSources unless
// the caller pre-populated opts.GhProvider (which only overrides the
// GitHub source — disk and clipboard sources are always built from
// opts.WsRoot/opts.Workspace). This keeps standalone `ws add` working
// out of the box while letting tests inject any subset.
func runTUI(ctx context.Context, opts Options) (*Result, error) {
	sources := buildSources(opts)

	// 10s per-source budget covers gh CLI paginate at ~300 repos
	// (observed: 7.5s for 294 repos via gh --paginate). Disk and
	// clipboard sources always finish well under this; the cap only
	// matters for github. Increase further if real users at 1k+
	// repos hit it — the TUI keeps spinning until ctx.Done either
	// way.
	model := NewAddModel(AddModelOptions{
		WsRoot:        opts.WsRoot,
		Workspace:     opts.Workspace,
		Save:          resolveSaveFn(opts),
		Sources:       sources,
		GatherTimeout: 10 * time.Second,
		Standalone:    true,
	})

	// Use AltScreen + signal handler so Ctrl+C surfaces as a clean
	// AddDoneMsg and the terminal restores correctly on quit.
	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
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

// buildSources assembles the default suggestion sources for a TUI run.
// Honors opts.GhProvider override but constructs disk and clipboard
// sources from the current environment (workspace, default reader).
//
// The GitHub source receives a workspace-derived `KnownRemotes` map so
// it can mark suggestions whose URL matches an already-registered
// project. The TUI uses that mark to render the "already cloned"
// highlight on the affected rows.
//
// Tests that need to swap sources construct their own AddModel directly
// (see tui_test.go); buildSources is the production wiring.
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

// knownRemotesFromWorkspace builds a "owner/repo" → project-path map
// from the workspace's registered projects. Used to flag GitHub
// suggestions whose remote already exists locally — the TUI then
// renders those rows with a "● cloned at <path>" suffix and a dimmed
// style so the user can see at a glance which suggestions would
// produce duplicates.
//
// Lower-cased keys so case differences in URLs (Foo/Bar vs foo/bar)
// still collide. Lossy on the rare case of two registered projects
// with the same upstream URL — last write wins, which is fine because
// the "already cloned" highlight is a hint, not a data integrity
// guarantee.
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

// ownerRepoFromRemote extracts a lowercased "owner/repo" from a git
// remote URL. Handles SSH shorthand (`git@host:owner/repo.git`) and
// scheme://host/owner/repo[.git] forms. Returns empty string when the
// shape doesn't match — the caller treats that as "no match".
func ownerRepoFromRemote(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	// SSH shorthand: git@host:owner/repo
	if at := strings.Index(s, "@"); at >= 0 && !strings.Contains(s, "://") {
		rest := s[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			s = rest[colon+1:] // owner/repo
			return strings.ToLower(s)
		}
	}

	// scheme://host/owner/repo
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if slash := strings.Index(s, "/"); slash >= 0 {
			s = s[slash+1:]
		}
	}

	// Strip any trailing path segments beyond owner/repo.
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return strings.ToLower(parts[0] + "/" + parts[1])
	}
	return ""
}

// resolveSaveFn returns opts.Save when set, else falls back to the
// production config.Save call. AddModel needs a non-nil saver because
// Register asserts on the field at the per-job level.
func resolveSaveFn(opts Options) func(*config.Workspace) error {
	if opts.Save != nil {
		return opts.Save
	}
	return func(ws *config.Workspace) error {
		return config.Save(opts.WsRoot, ws)
	}
}

// runHeadless walks Options.URLs, calling Register for each. It does
// NOT short-circuit on the first failure — failed URLs accumulate in
// Result.Errors so the user sees the whole story after one invocation.
// Between URLs, runHeadless re-checks ctx so a Ctrl+C mid-batch is
// honored promptly.
func runHeadless(ctx context.Context, opts Options) (*Result, error) {
	res := &Result{}
	for _, url := range opts.URLs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, err)
			return res, nil
		}
		// Reset per-URL options: Name is URL-specific, mustn't leak
		// across loop iterations even if the caller set it once.
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
