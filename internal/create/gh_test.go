package create

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeGHRunner replays canned outputs keyed by the prefix of the
// argv. The matcher is a simple "starts with these tokens" rule so a
// test can stub `gh api /user` without binding to the exact --jq flag.
type fakeGHRunner struct {
	calls    [][]string
	stdoutBy func(args []string) (string, error)
	stderrBy func(args []string) string
}

func (f *fakeGHRunner) Run(args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	var out string
	var runErr error
	if f.stdoutBy != nil {
		out, runErr = f.stdoutBy(args)
	}
	var stderr string
	if f.stderrBy != nil {
		stderr = f.stderrBy(args)
	}
	return []byte(out), []byte(stderr), runErr
}

func TestCurrentUser_OK(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			if !strings.Contains(strings.Join(args, " "), "/user") {
				return "", errors.New("unexpected args")
			}
			return "kuchmenko\n", nil
		},
	}
	got, err := CurrentUser(r)
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if got != "kuchmenko" {
		t.Errorf("got %q, want kuchmenko", got)
	}
}

func TestCurrentUser_AuthErr(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			return "", errors.New("exit status 4")
		},
		stderrBy: func(args []string) string {
			return "gh: not logged into github.com. Run gh auth login\n"
		},
	}
	_, err := CurrentUser(r)
	if !IsAuthErr(err) {
		t.Errorf("want auth err, got %v", err)
	}
}

func TestListOrgs_Multiline(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			return "example\nacme-corp\n\n", nil
		},
	}
	got, err := ListOrgs(r)
	if err != nil {
		t.Fatalf("ListOrgs: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"example", "acme-corp"}) {
		t.Errorf("got %v, want [example acme-corp]", got)
	}
}

func TestListOwners_PersonalFirst(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "/user/orgs"):
				return "alpha\nbeta\n", nil
			case strings.Contains(joined, "/user"):
				return "me\n", nil
			}
			return "", errors.New("unexpected args " + joined)
		},
	}
	got, err := ListOwners(r)
	if err != nil {
		t.Fatalf("ListOwners: %v", err)
	}
	want := []Owner{
		{Login: "me", Kind: OwnerKindUser},
		{Login: "alpha", Kind: OwnerKindOrg},
		{Login: "beta", Kind: OwnerKindOrg},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestCreateRepo_BuildsArgs_WithReadme(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			return "https://github.com/me/foo\n", nil
		},
	}
	url, err := CreateRepo(r, CreateRepoOptions{
		Owner:       "me",
		Name:        "foo",
		Visibility:  VisibilityPrivate,
		Description: "hello",
		AddReadme:   true,
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if url != "https://github.com/me/foo" {
		t.Errorf("url = %q, want https://github.com/me/foo", url)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	got := r.calls[0]
	want := []string{
		"repo", "create", "me/foo",
		"--private", "--clone=false", "--add-readme",
		"--description", "hello",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %#v\nwant %#v", got, want)
	}
}

func TestCreateRepo_PublicNoReadmeNoDesc(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			return "  https://github.com/org/bar  \nbanner: created\n", nil
		},
	}
	url, err := CreateRepo(r, CreateRepoOptions{
		Owner:      "org",
		Name:       "bar",
		Visibility: VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if url != "https://github.com/org/bar" {
		t.Errorf("url = %q", url)
	}
	want := []string{"repo", "create", "org/bar", "--public", "--clone=false"}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Errorf("argv = %#v\nwant %#v", r.calls[0], want)
	}
}

func TestCreateRepo_AlreadyExists(t *testing.T) {
	r := &fakeGHRunner{
		stdoutBy: func(args []string) (string, error) {
			return "", errors.New("exit status 1")
		},
		stderrBy: func(args []string) string {
			return "GraphQL error: Name already exists on this account\n"
		},
	}
	_, err := CreateRepo(r, CreateRepoOptions{
		Owner: "me", Name: "dup", Visibility: VisibilityPrivate,
	})
	if !IsRepoExistsErr(err) {
		t.Errorf("want repo-exists err, got %v", err)
	}
}

func TestCreateRepo_RejectsBadInputs(t *testing.T) {
	cases := []CreateRepoOptions{
		{Name: "x", Visibility: VisibilityPrivate},
		{Owner: "y", Visibility: VisibilityPrivate},
		{Owner: "y", Name: "x"},
		{Owner: "y", Name: "x", Visibility: "weird"},
	}
	for i, c := range cases {
		if _, err := CreateRepo(&fakeGHRunner{}, c); err == nil {
			t.Errorf("case %d: expected error for %+v", i, c)
		}
	}
}

func TestSSHURLFromOwnerRepo(t *testing.T) {
	if got := SSHURLFromOwnerRepo("kuchmenko", "ws"); got != "git@github.com:kuchmenko/ws.git" {
		t.Errorf("got %q", got)
	}
}
