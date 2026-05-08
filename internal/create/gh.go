// Package create implements the `ws create` command — bootstrap a new
// GitHub repository in a chosen account/org via the `gh` CLI, then
// register it in workspace.toml and clone it as bare+worktree.
//
// gh.go owns the read/write interactions with the GitHub side. Reads
// (current user, org list) hit `gh api`; the write (new repo) hits
// `gh repo create`. All gh invocations route through the runner
// interface so tests can swap in a fake without exec'ing real binaries.
package create

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Visibility maps to gh repo create flags. Empty string is rejected by
// CreateRepo — callers must pick one. Internal omitted: GitHub
// Enterprise-only and not relevant to this user's workflow.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

// Owner names a GitHub account that can hold a repo. Personal accounts
// and orgs share the same shape from the CLI's perspective; the kind
// is only used to render a "(you)" hint in the TUI.
type Owner struct {
	Login string
	Kind  OwnerKind
}

// OwnerKind distinguishes the user's personal account from orgs they
// can push to. The TUI surfaces this via a small chip; logic doesn't
// branch on it because `gh repo create <owner>/<name>` accepts both.
type OwnerKind string

const (
	OwnerKindUser OwnerKind = "user"
	OwnerKindOrg  OwnerKind = "org"
)

// CreateRepoOptions captures everything `gh repo create` needs. The
// runner translates these into a single argv. AddReadme defaults true
// for ws create (the resulting repo has a default branch + first
// commit, so clone.CloneIntoLayout doesn't trip on
// ErrNeedsBootstrap).
type CreateRepoOptions struct {
	Owner       string
	Name        string
	Visibility  Visibility
	Description string
	AddReadme   bool
}

// ghRunner is the seam tests inject. Production wiring is realGHRunner;
// tests pass a fake whose Run method asserts on argv and returns
// canned stdout/stderr.
//
// Returning a structured error (with stderr) is the runner's job — gh
// prints diagnostics on stderr which the caller maps to user-facing
// messages.
type ghRunner interface {
	Run(args ...string) (stdout []byte, stderr []byte, err error)
}

// realGHRunner shells out to the user's `gh` binary. Cheap to allocate;
// no shared state.
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

// errGHAuth signals that `gh` needs `gh auth login`. Wrapped so
// callers can present a single, actionable message instead of leaking
// the raw stderr.
var errGHAuth = errors.New("gh is not authenticated; run `gh auth login`")

// errRepoExists is returned by CreateRepo when GitHub rejects the
// request because <owner>/<name> already exists. Detected via stderr
// substring match because `gh repo create` doesn't surface a typed
// error code on this path.
var errRepoExists = errors.New("repository already exists on GitHub")

// CurrentUser returns the login of the authenticated `gh` user. Maps
// auth-related stderr ("not logged into") into errGHAuth so the caller
// can render a clean prompt instead of leaking stderr verbatim.
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

// ListOrgs returns the orgs the authenticated user can push to. `gh
// api /user/orgs` returns the membership list directly; we don't
// filter by role because gh already excludes orgs the token can't
// touch. --paginate handles >100 orgs without us doing math.
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

// ListOwners returns the personal account first, then orgs in the
// order gh returned them (which is stable per user). The TUI selector
// uses this order; the personal account at index 0 is the default
// highlight.
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

// CreateRepo runs `gh repo create` and returns the new repository's
// HTML URL on success. `--clone=false` is always passed because we
// drive the clone ourselves via clone.CloneIntoLayout.
//
// gh's stdout shape on success:
//
//	https://github.com/<owner>/<name>
//
// (single line, possibly trailing whitespace). Older `gh` may print a
// short banner; we take the first https://github.com/ line we find.
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

// SSHURLFromOwnerRepo returns the canonical SSH form for an owner/repo
// pair. We always register projects with their SSH URL so existing
// reconciler logic (which keys on remote.origin.url) sees a consistent
// shape regardless of what gh printed.
func SSHURLFromOwnerRepo(owner, name string) string {
	return fmt.Sprintf("git@github.com:%s/%s.git", owner, name)
}

// extractRepoURL pulls the github.com URL from gh's output. Tolerates
// banners or trailing whitespace by scanning lines.
func extractRepoURL(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://github.com/") {
			return line
		}
	}
	return ""
}

// classifyGHErr distinguishes auth failures from other gh errors. The
// match list comes from gh's actual stderr strings; gh doesn't expose
// a stable error-code surface, so substring match is what we have.
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

// isAlreadyExistsErr matches the GitHub API duplicate-name error. gh
// surfaces the API message verbatim on stderr; keying on "Name already
// exists" plus "name already exists on this account" covers both
// repo-already-exists and reserved-name responses.
func isAlreadyExistsErr(stderr []byte) bool {
	low := strings.ToLower(string(stderr))
	return strings.Contains(low, "name already exists")
}

// IsAuthErr reports whether err originated from `gh` not being
// authenticated. CLI/TUI use this to render a one-shot hint instead
// of stack-tracing the user.
func IsAuthErr(err error) bool {
	return errors.Is(err, errGHAuth)
}

// IsRepoExistsErr reports whether err signals that <owner>/<name> is
// already taken. The TUI catches this and lets the user edit the name
// without abandoning the form.
func IsRepoExistsErr(err error) bool {
	return errors.Is(err, errRepoExists)
}
