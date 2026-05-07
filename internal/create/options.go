package create

import "github.com/kuchmenko/workspace/internal/config"

// Mode controls TUI vs headless dispatch. Mirrors `ws add` — the same
// auto-detect rules apply (TTY without required flags → TUI).
type Mode int

const (
	// ModeAuto picks based on input completeness: TUI when Owner/Name
	// are missing on a TTY, headless when both are provided. Default.
	ModeAuto Mode = iota

	// ModeHeadless forces non-interactive. Errors if Owner or Name
	// is empty.
	ModeHeadless

	// ModeTUI forces the interactive form even on a non-TTY.
	ModeTUI
)

// Options is the union of every knob `ws create` exposes.
type Options struct {
	// Owner is the GitHub account/org the repo will live under.
	// Empty triggers TUI selection (or ErrNoOwner in headless).
	Owner string

	// Name is the new repository name. Validated against GitHub's
	// allowed character set before any gh call. Empty triggers TUI
	// (or ErrNoName in headless).
	Name string

	// Visibility is private|public. Empty defaults to private.
	Visibility Visibility

	// Description is forwarded to gh as --description. Optional.
	Description string

	// AddReadme controls whether to seed the repo with a README so
	// it has a default branch + first commit. Defaults true (set by
	// Run when zero-value). Without README, clone trips
	// ErrNeedsBootstrap.
	AddReadme *bool

	// Category for the workspace.toml entry. Empty → personal.
	Category config.Category

	// Group overrides the auto-inferred group. Empty → owner login
	// when category is work, else category.
	Group string

	// ProjectName overrides the derived project key in
	// workspace.toml. Empty → repo Name.
	ProjectName string

	// Mode selects TUI vs headless. See Mode.
	Mode Mode

	// WsRoot is the workspace root. Required.
	WsRoot string

	// Workspace is the in-memory toml state. Required.
	Workspace *config.Workspace

	// Save persists the workspace. Defaults to config.Save.
	Save func(*config.Workspace) error

	// GHRunner is injected by tests; nil → real `gh` exec.
	GHRunner ghRunner

	// URLFor builds the clone URL for a freshly-created repo. nil →
	// SSHURLFromOwnerRepo. Tests inject a closure that returns a
	// file:// URL pointing at a temp bare repo so the clone step
	// completes without a network round-trip.
	URLFor func(owner, name string) string
}

// Result describes a successful create operation. Returned only when
// the full pipeline (gh repo create → register → clone) completed.
// Partial failures surface as errors from Run.
type Result struct {
	Project config.Project
	Name    string
	URL     string // SSH URL the project was registered with
	Cloned  bool
}
