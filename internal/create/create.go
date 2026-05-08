package create

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
)

// ErrNoOwner is returned in headless mode when Options.Owner is empty.
// Headless cannot consult the user; the TUI is the usual answer to
// missing fields.
var ErrNoOwner = errors.New("no owner provided; pass --owner or run without --no-tui")

// ErrNoName is returned in headless mode when Options.Name is empty.
var ErrNoName = errors.New("no repo name provided; pass --name or run without --no-tui")

// ErrInvalidName is returned when Options.Name fails GitHub's
// allowed-character check (alphanumerics, -, _, .). Validated client
// side so the user sees the error before a gh round-trip.
var ErrInvalidName = errors.New("invalid repo name")

// nameRegex enforces the subset of GitHub's accepted names that we
// also want as project keys: starts with alphanumeric, then
// alphanumerics, dash, underscore, period. 1–100 chars. GitHub itself
// is slightly more permissive, but allowing leading "." or unusual
// chars complicates path derivation in workspace.toml.
var nameRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Run is the single entry point for `ws create`. Owns the sidecar
// lifecycle and dispatches on Mode:
//
//	ModeAuto with both Owner+Name set → headless
//	ModeAuto missing Owner or Name    → TUI
//	ModeHeadless missing fields       → ErrNoOwner / ErrNoName
//	ModeTUI                           → TUI regardless of fields
//
// On success, returns the Result describing the new project. The
// project is already registered in workspace.toml and cloned in
// bare+worktree form when Run returns.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WsRoot == "" {
		return nil, errors.New("create.Run: empty WsRoot")
	}
	if opts.Workspace == nil {
		return nil, errors.New("create.Run: nil Workspace")
	}

	useTUI := false
	switch opts.Mode {
	case ModeTUI:
		useTUI = true
	case ModeAuto:
		if opts.Owner == "" || opts.Name == "" {
			useTUI = true
		}
	case ModeHeadless:
		if opts.Owner == "" {
			return nil, ErrNoOwner
		}
		if opts.Name == "" {
			return nil, ErrNoName
		}
	default:
		return nil, fmt.Errorf("create.Run: unknown mode %d", opts.Mode)
	}

	// Sidecar acquire — pauses daemon, blocks concurrent ws create.
	if _, err := acquireSidecar(opts.WsRoot, opts.Mode, opts.Owner, opts.Name); err != nil {
		return nil, err
	}
	defer releaseSidecar(opts.WsRoot)

	if useTUI {
		return runTUI(ctx, opts)
	}
	return runHeadless(ctx, opts)
}

// runHeadless executes the create→register→clone pipeline using only
// Options fields. No prompts, no fallbacks — required fields missing
// is an error.
func runHeadless(ctx context.Context, opts Options) (*Result, error) {
	if err := validateName(opts.Name); err != nil {
		return nil, err
	}

	vis := opts.Visibility
	if vis == "" {
		vis = VisibilityPrivate
	}
	if vis != VisibilityPrivate && vis != VisibilityPublic {
		return nil, fmt.Errorf("invalid visibility %q (want private|public)", vis)
	}

	addReadme := true
	if opts.AddReadme != nil {
		addReadme = *opts.AddReadme
	}

	runner := opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := CreateRepo(runner, CreateRepoOptions{
		Owner:       opts.Owner,
		Name:        opts.Name,
		Visibility:  vis,
		Description: opts.Description,
		AddReadme:   addReadme,
	}); err != nil {
		return nil, fmt.Errorf("create repo: %w", err)
	}

	urlFor := opts.URLFor
	if urlFor == nil {
		urlFor = SSHURLFromOwnerRepo
	}
	sshURL := urlFor(opts.Owner, opts.Name)
	regOpts := buildRegisterOpts(opts)

	regRes, err := add.Register(regOpts, sshURL)
	if err != nil {
		// Clone failed but the GitHub repo exists — surface clearly so
		// the user can re-run `ws add` after fixing whatever blocked
		// the clone (auth, path conflict, etc).
		return nil, fmt.Errorf("repo created on GitHub at %s but local register failed: %w", sshURL, err)
	}

	return &Result{
		Project: regRes.Project,
		Name:    regRes.Name,
		URL:     sshURL,
		Cloned:  regRes.Cloned,
	}, nil
}

// buildRegisterOpts wires create.Options into add.Options for the
// final Register call. The transformations:
//
//   - ProjectName override → add.Options.Name (Register's name field)
//   - Category default → personal (Register also defaults but we want
//     a consistent value to surface in our own Result)
//   - Group default → owner login when category is work, else category.
//     Mirrors the legacy `ws setup` policy that grouped GitHub repos
//     by owner; for personal projects the flat "personal/<name>"
//     layout matches what `ws add` does today.
func buildRegisterOpts(opts Options) add.Options {
	cat := opts.Category
	if cat == "" {
		cat = config.CategoryPersonal
	}
	group := opts.Group
	if group == "" && cat == config.CategoryWork {
		group = opts.Owner
	}
	name := opts.ProjectName
	if name == "" {
		name = opts.Name
	}
	return add.Options{
		Category:  cat,
		Group:     group,
		Name:      name,
		WsRoot:    opts.WsRoot,
		Workspace: opts.Workspace,
		Save:      resolveSaveFn(opts),
	}
}

func resolveSaveFn(opts Options) func(*config.Workspace) error {
	if opts.Save != nil {
		return opts.Save
	}
	return func(ws *config.Workspace) error {
		return config.Save(opts.WsRoot, ws)
	}
}

// validateName enforces GitHub's accepted-name subset. The TUI calls
// this on every keystroke for inline error rendering; headless calls
// it once at the top of runHeadless.
func validateName(name string) error {
	if name == "" {
		return ErrNoName
	}
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q (allowed: A-Z, a-z, 0-9, ., -, _; 1-100 chars; must start with alphanumeric)", ErrInvalidName, name)
	}
	return nil
}
