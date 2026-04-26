package add

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

// ErrAlreadyRegistered is returned when a URL maps to a project name
// that already exists in Workspace.Projects. Distinguished from clone's
// ErrAlreadyCloned so callers can surface a different message ("try
// --name" rather than "already cloned").
var ErrAlreadyRegistered = errors.New("project already registered")

// RegisterResult carries the outcome of a single Register call.
type RegisterResult struct {
	Project config.Project
	Name    string
	Cloned  bool // true if we actually invoked CloneIntoLayout; false with --no-clone
}

// Register materializes one URL into a workspace.toml entry (and, by
// default, a bare+worktree clone). It is the single place that writes
// workspace.toml — both Run's headless loop and the TUI edit→confirm
// path funnel through here.
//
// The caller is responsible for supplying Options with WsRoot,
// Workspace, and Save set. Register does not acquire the sidecar — Run
// owns that lifecycle.
//
// Lifecycle:
//  1. Derive name (opts.Name override → git.ParseRepoName fallback).
//  2. Validate category, build relative path (group/name or category/name).
//  3. Reject if the name is already in Workspace.Projects.
//  4. Optionally CloneIntoLayout (unless opts.NoClone).
//  5. Mutate ws.Projects[name] = proj, persist via opts.Save.
//
// On CloneIntoLayout failure, Register does NOT save the workspace —
// the caller sees the error and no half-state lands in workspace.toml.
// If Save itself fails after a successful clone, the cloned bare+worktree
// stays on disk; the user can re-run `ws add` and the second invocation
// will detect clone.ErrAlreadyCloned and register only.
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
		// CloneIntoLayout mutates proj.DefaultBranch on success.
		// Pass no PromptDefaultBranch — Register is non-interactive
		// by contract; ambiguous defaults surface as
		// clone.ErrNeedsBootstrap and the caller is told to run
		// `ws bootstrap <name>` afterwards.
		_, err := clone.CloneIntoLayout(opts.WsRoot, name, &proj, clone.Options{})
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

// inferGroup picks a group label for a Suggestion when the caller
// hasn't supplied an explicit Group. Called from headless registration
// (where group is rarely set on the CLI). For TUI registrations the
// edit screen pre-fills from the suggestion's InferredGrp (typically
// the GitHub owner) and the user can override before confirm —
// inferGroup is the fallback for when no signal is available.
//
// Current policy is simple: group == string(category). The legacy
// `ws setup` step_group.go grouped GitHub repos by owner; this
// minimal version preserves the contract (every project has a
// non-empty group) without dragging in the org-resolution
// scaffolding. The TUI's edit screen handles richer cases.
func inferGroup(_ string, cat config.Category) string {
	return string(cat)
}

// buildPath assembles the workspace-relative directory for a project.
// Preserves the legacy `ws add` behavior: group=="" falls through to
// `<category>/<name>`; explicit group trumps category.
func buildPath(group string, cat config.Category, name string) string {
	if group != "" {
		return filepath.Join(group, name)
	}
	return filepath.Join(string(cat), name)
}

// describeCloneErr turns clone sentinel errors into user-facing hints.
// Called by Run when assembling per-URL error messages.
func describeCloneErr(err error, name string) string {
	switch {
	case errors.Is(err, clone.ErrAlreadyCloned):
		return fmt.Sprintf("%s: already cloned at destination", name)
	case errors.Is(err, clone.ErrNeedsMigration):
		return fmt.Sprintf("%s: existing plain checkout — run `ws migrate %s`", name, name)
	case errors.Is(err, clone.ErrPathBlocked):
		return fmt.Sprintf("%s: non-repo files at destination path", name)
	case errors.Is(err, clone.ErrNeedsBootstrap):
		return fmt.Sprintf("%s: default branch ambiguous — run `ws bootstrap %s` to pick one", name, name)
	default:
		return fmt.Sprintf("%s: %s", name, strings.TrimSpace(err.Error()))
	}
}
