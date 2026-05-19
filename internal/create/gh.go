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
