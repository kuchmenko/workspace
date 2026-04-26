package add

import (
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/github"
)

// Mode controls whether Run presents a TUI or runs headless.
type Mode int

const (
	// ModeAuto picks based on runtime: TUI when stdin is a TTY and
	// no URLs are given; headless otherwise. Default for `ws add`.
	ModeAuto Mode = iota

	// ModeHeadless forces the non-interactive path. Set by `--no-tui`
	// or selected automatically when stdin is not a TTY.
	ModeHeadless

	// ModeTUI forces the TUI even on a non-TTY (rarely useful;
	// included for symmetry with --no-tui and to let the future
	// embed path opt in explicitly).
	ModeTUI

	// ModeEmbedded is used when `ws add` is hosted inside another
	// bubbletea program (the agent TUI). The caller is responsible
	// for the parent tea.Program lifecycle; Run does not create its
	// own. Currently returns ErrEmbedNotSupported — the embedded
	// path lands with the agent integration.
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
	// suggestions (TUI) or errors (headless).
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

	// GhProvider is the GitHub suggestion backend. nil →
	// github.ResolveProvider(). Override in tests or to inject a
	// stubbed provider.
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
