package add

import (
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/github"
)

// Mode controls whether Run presents a TUI or runs headless.
//
// Phase 1 implements only ModeHeadless. ModeAuto, ModeTUI, and
// ModeEmbedded land with Phase 3 (TUI) and Phase 5 (agent embed).
type Mode int

const (
	// ModeAuto picks based on runtime: TUI when stdin is a TTY and
	// no URLs are given; headless otherwise. Default for `ws add`.
	ModeAuto Mode = iota

	// ModeHeadless forces the non-interactive path. Set by `--no-tui`
	// or selected automatically when stdin is not a TTY.
	ModeHeadless

	// ModeTUI forces the TUI. Errors in Phase 1-C with "TUI ships in
	// Phase 3" — the skeleton accepts the value to lock the shape
	// before the real implementation lands.
	ModeTUI

	// ModeEmbedded is used when `ws add` is hosted inside another
	// bubbletea program (the agent TUI in Phase 5). The caller is
	// responsible for the parent tea.Program lifecycle; Run does
	// not create its own. Phase 1-C returns ErrEmbedNotSupported.
	ModeEmbedded
)

// Options is the union of every knob `ws add` exposes. CLI and agent
// callers populate the fields they care about; all others take sane
// defaults. Runtime-dependency fields (GhProvider, ClipboardImpl,
// DiskRoots, Save) are nil-able — the zero value triggers production
// defaults, tests inject doubles.
type Options struct {
	// Inputs.

	// URLs lists positional git-remote URLs. Empty → Run gathers
	// suggestions (TUI) or errors (headless). In Phase 1-C only the
	// non-empty-URLs headless path is wired.
	URLs []string

	// Category is the `Projects[*].Category` field to write. Empty →
	// config.CategoryPersonal.
	Category config.Category

	// Group overrides the auto-inferred group. Empty → inferGroup.
	Group string

	// Name overrides the derived repo name. Empty → git.ParseRepoName.
	Name string

	// NoClone writes the TOML entry without cloning. Useful for
	// pre-registering a project whose remote will become available
	// later. Disk-source entries do not honor this flag (they are
	// already cloned).
	NoClone bool

	// Mode selects TUI vs headless. See Mode.
	Mode Mode

	// Runtime + injection.

	// WsRoot is the workspace root. Required; Run errors on empty.
	WsRoot string

	// Workspace is the in-memory toml state. Required; Run errors on nil.
	Workspace *config.Workspace

	// Save persists the workspace. Defaults to config.Save(WsRoot, ws).
	// Injected for tests.
	Save func(*config.Workspace) error

	// GhProvider is the GitHub suggestion backend. nil → github.ResolveProvider().
	// Not used in Phase 1-C (suggestions land in Phase 3), but the
	// field is present so callers can wire it once and forget.
	GhProvider github.Provider
}

// Result summarizes what Run did. Always non-nil; check Errors for
// partial-failure cases.
type Result struct {
	// Added are the projects successfully registered and, if relevant,
	// cloned. One entry per URL for headless multi-add.
	Added []config.Project

	// Skipped records URLs that were intentionally skipped (e.g.
	// already registered, or the user chose "skip" in the TUI).
	Skipped []SkipReason

	// Errors collects per-URL failures. Run returns a non-nil error
	// only when the whole operation failed (e.g. sidecar-acquire
	// conflict); individual per-URL failures land here instead.
	Errors []error
}

// SkipReason explains why Run did not register a URL.
type SkipReason struct {
	URL    string
	Reason string
}
