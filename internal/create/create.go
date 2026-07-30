package create

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/sidecar"
	"github.com/kuchmenko/workspace/internal/tui"
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

type ownersLoadedMsg struct{ owners []Owner }
type ownersErrMsg struct{ err error }
type createDoneMsg struct{ result *Result }
type createErrMsg struct{ err error }

func (m CreateModel) fetchOwnersCmd() tui.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}
	return func() tui.Msg {
		owners, err := ListOwners(runner)
		if err != nil {
			return ownersErrMsg{err: err}
		}
		return ownersLoadedMsg{owners: owners}
	}
}

func (m CreateModel) createCmd() tui.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}

	owner := m.currentOwner()
	name := strings.TrimSpace(m.nameInput.Value())
	desc := strings.TrimSpace(m.descInput.Value())
	visibility := m.visibilities[m.visIdx]
	category := m.categories[m.catIdx]
	group := strings.TrimSpace(m.groupInput.Value())

	wsRoot := m.opts.WsRoot
	ws := m.opts.Workspace
	saveFn := m.opts.Save
	if saveFn == nil {
		saveFn = func(w *config.Workspace) error { return config.Save(wsRoot, w) }
	}
	projectName := m.opts.ProjectName
	if projectName == "" {
		projectName = name
	}

	return func() tui.Msg {
		if _, err := CreateRepo(runner, CreateRepoOptions{
			Owner:       owner,
			Name:        name,
			Visibility:  visibility,
			Description: desc,
			AddReadme:   true,
		}); err != nil {
			return createErrMsg{err: fmt.Errorf("create repo: %w", err)}
		}

		urlFor := m.opts.URLFor
		if urlFor == nil {
			urlFor = SSHURLFromOwnerRepo
		}
		sshURL := urlFor(owner, name)
		regOpts := add.Options{
			Category:  category,
			Group:     group,
			Name:      projectName,
			WsRoot:    wsRoot,
			Workspace: ws,
			Save:      saveFn,
		}
		regRes, err := add.Register(regOpts, sshURL)
		if err != nil {
			return createErrMsg{
				err: fmt.Errorf("repo created on GitHub at %s but register failed: %w", sshURL, err),
			}
		}

		return createDoneMsg{
			result: &Result{
				Project: regRes.Project,
				Name:    regRes.Name,
				URL:     sshURL,
				Cloned:  regRes.Cloned,
			},
		}
	}
}

type Mode int

const (
	ModeAuto Mode = iota

	ModeHeadless

	ModeTUI
)

type Options struct {
	Owner string

	Name string

	Visibility Visibility

	Description string

	AddReadme *bool

	Category config.Category

	Group string

	ProjectName string

	Mode Mode

	WsRoot string

	Workspace *config.Workspace

	Save func(*config.Workspace) error

	GHRunner ghRunner

	URLFor func(owner, name string) string
}

type Result struct {
	Project config.Project
	Name    string
	URL     string
	Cloned  bool
}

var ErrCancelled = errors.New("create canceled by user")

func runTUI(ctx context.Context, opts Options) (*Result, error) {
	model := NewCreateModel(CreateModelOptions{
		WsRoot:      opts.WsRoot,
		Workspace:   opts.Workspace,
		Save:        resolveSaveFn(opts),
		GHRunner:    opts.GHRunner,
		Owner:       opts.Owner,
		Name:        opts.Name,
		Visibility:  opts.Visibility,
		Description: opts.Description,
		Category:    opts.Category,
		Group:       opts.Group,
		ProjectName: opts.ProjectName,
		URLFor:      opts.URLFor,
	})

	prog := tui.NewProgram(
		model,
		tui.WithAltScreen(),
		tui.WithContext(ctx),
	)
	finalModel, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("create TUI: %w", err)
	}
	final, ok := finalModel.(CreateModel)
	if !ok {
		return nil, fmt.Errorf("create TUI: unexpected final model type %T", finalModel)
	}
	if final.canceled {
		return nil, ErrCancelled
	}
	if final.err != nil {
		return nil, final.err
	}
	if final.result == nil {
		return nil, errors.New("create TUI exited with no result")
	}
	return final.result, nil
}

type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

type Owner struct {
	Login string
	Kind  OwnerKind
}

type OwnerKind string

const (
	OwnerKindUser OwnerKind = "user"
	OwnerKindOrg  OwnerKind = "org"
)

type CreateRepoOptions struct {
	Owner       string
	Name        string
	Visibility  Visibility
	Description string
	AddReadme   bool
}

type ghRunner interface {
	Run(args ...string) (stdout []byte, stderr []byte, err error)
}

type realGHRunner struct{}

func (realGHRunner) Run(args ...string) ([]byte, []byte, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, exitErr.Stderr, err
		}
		return out, nil, err
	}
	return out, nil, nil
}

var errGHAuth = errors.New("gh is not authenticated; run `gh auth login`")

var errRepoExists = errors.New("repository already exists on GitHub")

func CurrentUser(r ghRunner) (string, error) {
	out, stderr, err := r.Run("api", "/user", "--jq", ".login")
	if err != nil {
		return "", classifyGHErr(stderr, err)
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return "", errors.New("gh api /user returned empty login")
	}
	return login, nil
}

func ListOrgs(r ghRunner) ([]string, error) {
	out, stderr, err := r.Run("api", "/user/orgs?per_page=100", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, classifyGHErr(stderr, err)
	}
	var orgs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			orgs = append(orgs, line)
		}
	}
	return orgs, nil
}

func ListOwners(r ghRunner) ([]Owner, error) {
	user, err := CurrentUser(r)
	if err != nil {
		return nil, err
	}
	orgs, err := ListOrgs(r)
	if err != nil {
		return nil, err
	}
	owners := make([]Owner, 0, 1+len(orgs))
	owners = append(owners, Owner{Login: user, Kind: OwnerKindUser})
	for _, o := range orgs {
		owners = append(owners, Owner{Login: o, Kind: OwnerKindOrg})
	}
	return owners, nil
}

func CreateRepo(r ghRunner, opts CreateRepoOptions) (string, error) {
	if opts.Owner == "" {
		return "", errors.New("CreateRepo: empty Owner")
	}
	if opts.Name == "" {
		return "", errors.New("CreateRepo: empty Name")
	}
	if opts.Visibility != VisibilityPrivate && opts.Visibility != VisibilityPublic {
		return "", fmt.Errorf("CreateRepo: invalid visibility %q", opts.Visibility)
	}

	args := []string{
		"repo", "create",
		fmt.Sprintf("%s/%s", opts.Owner, opts.Name),
		"--" + string(opts.Visibility),
		"--clone=false",
	}
	if opts.AddReadme {
		args = append(args, "--add-readme")
	}
	if opts.Description != "" {
		args = append(args, "--description", opts.Description)
	}

	out, stderr, err := r.Run(args...)
	if err != nil {
		if isAlreadyExistsErr(stderr) {
			return "", errRepoExists
		}
		return "", classifyGHErr(stderr, err)
	}
	url := extractRepoURL(string(out))
	if url == "" {
		return "", fmt.Errorf("could not parse repo URL from gh output: %q", strings.TrimSpace(string(out)))
	}
	return url, nil
}

func SSHURLFromOwnerRepo(owner, name string) string {
	return fmt.Sprintf("git@github.com:%s/%s.git", owner, name)
}

func extractRepoURL(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://github.com/") {
			return line
		}
	}
	return ""
}

func classifyGHErr(stderr []byte, base error) error {
	msg := string(stderr)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "not logged into"),
		strings.Contains(low, "authentication required"),
		strings.Contains(low, "401"),
		strings.Contains(low, "must authenticate"):
		return errGHAuth
	}
	if msg != "" {
		return fmt.Errorf("%w: %s", base, strings.TrimSpace(msg))
	}
	return base
}

func isAlreadyExistsErr(stderr []byte) bool {
	low := strings.ToLower(string(stderr))
	return strings.Contains(low, "name already exists")
}

func IsAuthErr(err error) bool {
	return errors.Is(err, errGHAuth)
}

func IsRepoExistsErr(err error) bool {
	return errors.Is(err, errRepoExists)
}

type sidecarPayload struct {
	Mode  Mode   `json:"mode"`
	Owner string `json:"owner,omitempty"`
	Name  string `json:"name,omitempty"`
}

const sidecarPayloadKey = "__session__"

func acquireSidecar(wsRoot string, mode Mode, owner, name string) (*sidecar.Sidecar, error) {
	existing, err := sidecar.Load(wsRoot, sidecar.KindCreate)
	if err != nil {
		return nil, fmt.Errorf("read create sidecar: %w", err)
	}
	if existing != nil {
		if sidecar.IsAlive(existing) {
			var pay sidecarPayload
			_, _ = existing.Get(sidecarPayloadKey, &pay)
			return nil, fmt.Errorf(
				"another `ws create` is running (pid %d, started %s, %s)",
				existing.Meta.PID,
				existing.Meta.Started.Local().Format(time.RFC3339),
				describePayload(pay),
			)
		}
		if err := sidecar.Delete(wsRoot, sidecar.KindCreate); err != nil {
			return nil, fmt.Errorf("clear stale create sidecar: %w", err)
		}
	}

	sc := sidecar.New(wsRoot, sidecar.KindCreate)
	if err := sc.Set(sidecarPayloadKey, sidecarPayload{Mode: mode, Owner: owner, Name: name}); err != nil {
		return nil, fmt.Errorf("encode sidecar payload: %w", err)
	}
	if err := sidecar.Save(sc); err != nil {
		return nil, fmt.Errorf("save create sidecar: %w", err)
	}
	return sc, nil
}

func releaseSidecar(wsRoot string) {
	_ = sidecar.Delete(wsRoot, sidecar.KindCreate)
}

func describePayload(p sidecarPayload) string {
	modeName := "auto"
	switch p.Mode {
	case ModeHeadless:
		modeName = "headless"
	case ModeTUI:
		modeName = "tui"
	}
	if p.Owner != "" && p.Name != "" {
		return fmt.Sprintf("%s mode, creating %s/%s", modeName, p.Owner, p.Name)
	}
	return modeName + " mode"
}
