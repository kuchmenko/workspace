package create

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
)

var ErrNoOwner = errors.New("no owner provided; pass --owner or run without --no-tui")

var ErrNoName = errors.New("no repo name provided; pass --name or run without --no-tui")

var ErrInvalidName = errors.New("invalid repo name")

var nameRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

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

	if _, err := acquireSidecar(opts.WsRoot, opts.Mode, opts.Owner, opts.Name); err != nil {
		return nil, err
	}
	defer releaseSidecar(opts.WsRoot)

	if useTUI {
		return runTUI(ctx, opts)
	}
	return runHeadless(ctx, opts)
}

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
		return nil, fmt.Errorf("repo created on GitHub at %s but local register failed: %w", sshURL, err)
	}

	return &Result{
		Project: regRes.Project,
		Name:    regRes.Name,
		URL:     sshURL,
		Cloned:  regRes.Cloned,
	}, nil
}

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

func validateName(name string) error {
	if name == "" {
		return ErrNoName
	}
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q (allowed: A-Z, a-z, 0-9, ., -, _; 1-100 chars; must start with alphanumeric)", ErrInvalidName, name)
	}
	return nil
}
