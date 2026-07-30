package add

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/github"
	"github.com/kuchmenko/workspace/internal/sidecar"
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
		&ClipboardSource{Reader: DefaultClipboardReader},
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

var ErrAlreadyRegistered = errors.New("project already registered")

type RegisterResult struct {
	Project config.Project
	Name    string
	Cloned  bool
}

func Register(opts Options, url string) (*RegisterResult, error) {
	if opts.WsRoot == "" {
		return nil, errors.New("register: empty WsRoot")
	}
	if opts.Workspace == nil {
		return nil, errors.New("register: nil Workspace")
	}

	name := opts.Name
	if name == "" {
		name = git.ParseRepoName(url)
	}
	if name == "" {
		return nil, fmt.Errorf("register: could not derive project name from %q", url)
	}

	if _, exists := opts.Workspace.Projects[name]; exists {
		return nil, fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}

	cat := opts.Category
	if cat == "" {
		cat = config.CategoryPersonal
	}
	if cat != config.CategoryPersonal && cat != config.CategoryWork {
		return nil, fmt.Errorf("register: category must be personal|work, got %q", cat)
	}

	group := opts.Group
	if group == "" {
		group = inferGroup(url, cat)
	}

	relPath := buildPath(group, cat, name)

	proj := config.Project{
		Remote:   url,
		Path:     relPath,
		Status:   config.StatusActive,
		Category: cat,
		Group:    group,
	}

	cloned := false
	if !opts.NoClone {
		_, err := git.CloneIntoLayout(opts.WsRoot, name, &proj, git.CloneOptions{})
		if err != nil {
			return nil, fmt.Errorf("clone %s: %w", name, err)
		}
		cloned = true
	}

	if opts.Workspace.Projects == nil {
		opts.Workspace.Projects = make(map[string]config.Project)
	}
	opts.Workspace.Projects[name] = proj

	saveFn := opts.Save
	if saveFn == nil {
		saveFn = func(ws *config.Workspace) error {
			return config.Save(opts.WsRoot, ws)
		}
	}
	if err := saveFn(opts.Workspace); err != nil {
		return nil, fmt.Errorf("save workspace.toml: %w", err)
	}

	return &RegisterResult{Project: proj, Name: name, Cloned: cloned}, nil
}

func inferGroup(_ string, cat config.Category) string {
	return string(cat)
}

func buildPath(group string, cat config.Category, name string) string {
	if group != "" {
		return filepath.Join(group, name)
	}
	return filepath.Join(string(cat), name)
}

type Mode int

const (
	ModeAuto Mode = iota

	ModeHeadless

	ModeTUI

	ModeEmbedded
)

type Options struct {
	URLs []string

	Category config.Category

	Group string

	Name string

	NoClone bool

	Mode Mode

	WsRoot string

	Workspace *config.Workspace

	Save func(*config.Workspace) error

	GhProvider github.Provider
}

type Result struct {
	Added []config.Project

	Skipped []SkipReason

	Errors []error
}

type SkipReason struct {
	URL    string
	Reason string
}

type AddDoneMsg struct {
	Added   []config.Project
	Skipped []SkipReason
	Errors  []error
}

type cloneDoneMsg struct {
	idx     int
	project config.Project
	skipped *SkipReason
	err     error
}

type allClonesDoneMsg struct{}

type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

type sourceDoneMsg struct {
	name  string
	items []Suggestion
	err   error
	took  time.Duration
}

type sidecarPayload struct {
	Mode Mode     `json:"mode"`
	URLs []string `json:"urls,omitempty"`
}

const sidecarPayloadKey = "__session__"

func acquireSidecar(wsRoot string, mode Mode, urls []string) (*sidecar.Sidecar, error) {
	existing, err := sidecar.Load(wsRoot, sidecar.KindAdd)
	if err != nil {
		return nil, fmt.Errorf("read add sidecar: %w", err)
	}
	if existing != nil {
		if sidecar.IsAlive(existing) {
			var pay sidecarPayload
			_, _ = existing.Get(sidecarPayloadKey, &pay)
			return nil, fmt.Errorf(
				"another `ws add` is running (pid %d, started %s, %s)",
				existing.Meta.PID,
				existing.Meta.Started.Local().Format(time.RFC3339),
				describePayload(pay),
			)
		}

		if err := sidecar.Delete(wsRoot, sidecar.KindAdd); err != nil {
			return nil, fmt.Errorf("clear stale add sidecar: %w", err)
		}
	}

	sc := sidecar.New(wsRoot, sidecar.KindAdd)
	if err := sc.Set(sidecarPayloadKey, sidecarPayload{Mode: mode, URLs: urls}); err != nil {
		return nil, fmt.Errorf("encode sidecar payload: %w", err)
	}
	if err := sidecar.Save(sc); err != nil {
		return nil, fmt.Errorf("save add sidecar: %w", err)
	}
	return sc, nil
}

func releaseSidecar(wsRoot string) {
	_ = sidecar.Delete(wsRoot, sidecar.KindAdd)
}

func describePayload(p sidecarPayload) string {
	modeName := "auto"
	switch p.Mode {
	case ModeHeadless:
		modeName = "headless"
	case ModeTUI:
		modeName = "tui"
	case ModeEmbedded:
		modeName = "embedded"
	}
	if len(p.URLs) == 0 {
		return modeName + " mode"
	}
	if len(p.URLs) == 1 {
		return fmt.Sprintf("%s mode, adding %s", modeName, p.URLs[0])
	}
	return fmt.Sprintf("%s mode, adding %d URLs: %s",
		modeName, len(p.URLs), strings.Join(p.URLs, ", "))
}
