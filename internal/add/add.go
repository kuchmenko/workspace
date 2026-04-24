// Package add is the shared core behind `ws add` (standalone),
// `ws bootstrap` on an empty workspace.toml (Phase 4), and the
// `ws agent` add screen (Phase 5). All three callers funnel through
// Run(ctx, Options).
//
// Phase 1-C implements the headless path only: Run takes explicit URLs
// via Options.URLs and registers each one via Register. The TUI path
// (ModeTUI, ModeAuto with TTY) lands in Phase 3 and will reuse this
// same Run entry point — a caller with Options.URLs=nil and Mode=ModeTUI
// will transfer control to the future TUI model, which eventually calls
// Register for each confirmed selection.
//
// The sidecar at ~/.local/state/ws/add/<sha>.toml is acquired before
// any workspace.toml mutation and released on every exit path (defer).
// The daemon reconciler's AnyActive sweep now includes KindAdd, so a
// running `ws add` pauses the daemon for the affected workspace — no
// interleaving writes.
package add

import (
	"context"
	"errors"
	"fmt"
)

// ErrTUINotImplemented signals a caller explicitly requested ModeTUI
// before the TUI ships in Phase 3. Callers (CLI, agent) inspect this
// sentinel and either print a helpful message or fall back to headless.
var ErrTUINotImplemented = errors.New("`ws add` TUI ships in Phase 3; pass URLs positionally or use --no-tui")

// ErrEmbedNotSupported is returned when a caller asks for ModeEmbedded.
// Phase 5 (agent integration) will implement embed; Phase 1 shape
// freezes the interface without providing the behavior.
var ErrEmbedNotSupported = errors.New("embedded mode ships in Phase 5 (ws agent)")

// ErrNoURLs is returned by headless runs with Options.URLs empty.
// The TUI is the usual answer to "I didn't bring URLs"; headless by
// definition cannot consult the user.
var ErrNoURLs = errors.New("no URLs provided; pass one or more git remote URLs")

// Run is the single entry point for every `ws add` style operation.
// It owns the sidecar lifecycle and dispatches on Mode to the
// appropriate implementation.
//
// Phase 1-C behavior:
//
//	ModeAuto     with URLs      → headless
//	ModeAuto     without URLs   → ErrTUINotImplemented
//	ModeHeadless with URLs      → headless
//	ModeHeadless without URLs   → ErrNoURLs
//	ModeTUI                      → ErrTUINotImplemented
//	ModeEmbedded                 → ErrEmbedNotSupported
//
// Phase 3 promotes TUI + Auto-without-URLs. Phase 5 promotes Embedded.
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

	switch opts.Mode {
	case ModeTUI:
		return nil, ErrTUINotImplemented
	case ModeEmbedded:
		return nil, ErrEmbedNotSupported
	case ModeAuto:
		if len(opts.URLs) == 0 {
			return nil, ErrTUINotImplemented
		}
		// ModeAuto with URLs → headless.
	case ModeHeadless:
		if len(opts.URLs) == 0 {
			return nil, ErrNoURLs
		}
	default:
		return nil, fmt.Errorf("add.Run: unknown mode %d", opts.Mode)
	}

	// Sidecar acquire (blocks concurrent `ws add`, pauses the daemon).
	sc, err := acquireSidecar(opts.WsRoot, opts.Mode, opts.URLs)
	if err != nil {
		return nil, err
	}
	_ = sc // currently unused after acquire; progress tracking comes in Phase 3
	defer releaseSidecar(opts.WsRoot)

	return runHeadless(ctx, opts)
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
