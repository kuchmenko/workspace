package git_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/testutil"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		want       git.Remote
		wantSource string
	}{
		{
			name: "https",
			raw:  "https://User:secret@GitHub.com/owner/project.git",
			want: git.Remote{
				Raw:        "https://User:secret@GitHub.com/owner/project.git",
				Scheme:     "https",
				User:       "User",
				Host:       "github.com",
				Repository: "owner/project",
			},
			wantSource: "https://github.com:443",
		},
		{
			name: "scp ssh",
			raw:  "git@GitLab.com:group/project.git",
			want: git.Remote{
				Raw:        "git@GitLab.com:group/project.git",
				Scheme:     "ssh",
				User:       "git",
				Host:       "gitlab.com",
				Repository: "group/project",
			},
			wantSource: "ssh://git@gitlab.com:22",
		},
		{
			name: "ssh URL",
			raw:  "ssh://deploy@codeberg.org:2222/team/project.git",
			want: git.Remote{
				Raw:        "ssh://deploy@codeberg.org:2222/team/project.git",
				Scheme:     "ssh",
				User:       "deploy",
				Host:       "codeberg.org",
				Port:       "2222",
				Repository: "team/project",
			},
			wantSource: "ssh://deploy@codeberg.org:2222",
		},
		{
			name: "file URL",
			raw:  "file:///tmp/repos/project.git",
			want: git.Remote{
				Raw:        "file:///tmp/repos/project.git",
				Scheme:     "file",
				Repository: "/tmp/repos/project.git",
			},
			wantSource: "local",
		},
		{
			name: "local path",
			raw:  "../repos/project.git",
			want: git.Remote{
				Raw:        "../repos/project.git",
				Scheme:     "local",
				Repository: "../repos/project.git",
			},
			wantSource: "local",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := git.ParseRemote(test.raw)
			if err != nil {
				t.Fatalf("ParseRemote: %v", err)
			}
			if got != test.want {
				t.Fatalf("ParseRemote() = %#v, want %#v", got, test.want)
			}
			if got.SourceKey() != test.wantSource {
				t.Errorf("SourceKey() = %q, want %q", got.SourceKey(), test.wantSource)
			}
		})
	}
}

func TestRepositoryKeyNormalizesHostedTransports(t *testing.T) {
	remotes := []string{
		"https://github.com/owner/project.git",
		"git@github.com:owner/project.git",
		"ssh://git@github.com/owner/project",
	}
	for _, raw := range remotes {
		remote, err := git.ParseRemote(raw)
		if err != nil {
			t.Fatalf("ParseRemote(%q): %v", raw, err)
		}
		if got, want := remote.RepositoryKey(), "github.com/owner/project"; got != want {
			t.Errorf("RepositoryKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRemoteRedaction(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://token@github.com/owner/project.git", "https://REDACTED@github.com/owner/project.git"},
		{"https://user:password@gitlab.com/owner/project.git", "https://REDACTED@gitlab.com/owner/project.git"},
		{"ssh://git:password@codeberg.org/owner/project.git", "ssh://git:REDACTED@codeberg.org/owner/project.git"},
		{"git@github.com:owner/project.git", "git@github.com:owner/project.git"},
	}
	for _, test := range tests {
		if got := git.RedactRemote(test.raw); got != test.want {
			t.Errorf("RedactRemote(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestRedactDiagnosticRemovesCredentials(t *testing.T) {
	raw := "https://user:password@example.com/owner/project.git"
	diagnostic := "fatal: unable to access '" + raw + "': token password"
	got := git.RedactDiagnostic(diagnostic, raw)
	if strings.Contains(got, "user") || strings.Contains(got, "password@example") || strings.Contains(got, raw) {
		t.Fatalf("RedactDiagnostic leaked credentials: %q", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("RedactDiagnostic = %q, want redaction marker", got)
	}
}

func TestResolveRemoteURLUsesExplicitBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repository.git")
	got, err := git.ResolveRemoteURL("../remotes/project.git", base)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(base), "remotes", "project.git")
	if got != want {
		t.Fatalf("ResolveRemoteURL = %q, want %q", got, want)
	}
}

func TestKnownHostSSHCandidate(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://github.com/owner/project", "git@github.com:owner/project.git", true},
		{"https://gitlab.com/group/project.git", "git@gitlab.com:group/project.git", true},
		{"https://codeberg.org/team/project.git", "git@codeberg.org:team/project.git", true},
		{"https://example.com/team/project.git", "", false},
		{"https://github.com:8443/team/project.git", "", false},
		{"git@github.com:owner/project.git", "", false},
	}
	for _, test := range tests {
		got, ok := git.KnownHostSSHCandidate(test.raw)
		if got != test.want || ok != test.ok {
			t.Errorf("KnownHostSSHCandidate(%q) = (%q, %v), want (%q, %v)", test.raw, got, ok, test.want, test.ok)
		}
	}
}

func TestParseRemoteRejectsInvalidURLs(t *testing.T) {
	for _, raw := range []string{"", "http://github.com/owner/project", "https://github.com", "file://server/repo.git"} {
		if _, err := git.ParseRemote(raw); err == nil {
			t.Errorf("ParseRemote(%q) succeeded", raw)
		}
	}
}

func TestProbeRepository(t *testing.T) {
	remote := testutil.InitFakeRemote(t, "probe", "main")
	if err := git.ProbeRepository(context.Background(), remote); err != nil {
		t.Fatalf("ProbeRepository: %v", err)
	}
}

func TestProbeRepositoryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := git.ProbeRepository(ctx, filepath.Join(t.TempDir(), "missing.git"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeRepository error = %v, want context.Canceled", err)
	}
}

func TestProbeRepositoryErrorRedactsCredentials(t *testing.T) {
	raw := "https://user:secret@127.0.0.1:1/owner/project.git"
	err := git.ProbeRepository(context.Background(), raw)
	if err == nil {
		t.Fatal("ProbeRepository unexpectedly succeeded")
	}
	diagnostic := err.Error()
	if strings.Contains(diagnostic, raw) || strings.Contains(diagnostic, "secret") || strings.Contains(diagnostic, "user:") {
		t.Fatalf("ProbeRepository error leaked credentials: %q", diagnostic)
	}
}
