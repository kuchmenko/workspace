package git

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Remote struct {
	Raw        string
	Scheme     string
	User       string
	Host       string
	Port       string
	Repository string
}

var scpRemotePattern = regexp.MustCompile(`^(?:([^@/:]+)@)?([^/:]+):(.+)$`)

func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Remote{}, fmt.Errorf("empty git remote")
	}
	if strings.Contains(raw, "://") {
		return parseURLRemote(raw)
	}
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil && !filepath.IsAbs(raw) {
		return Remote{Raw: raw, Scheme: "ssh", User: match[1], Host: strings.ToLower(match[2]), Repository: cleanHostedRepository(match[3])}, nil
	}
	return Remote{Raw: raw, Scheme: "local", Repository: filepath.Clean(raw)}, nil
}

func parseURLRemote(raw string) (Remote, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return Remote{}, fmt.Errorf("invalid git remote")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "ssh" && scheme != "file" {
		return Remote{}, fmt.Errorf("unsupported git remote scheme %q", scheme)
	}
	remote := Remote{
		Raw:    raw,
		Scheme: scheme,
		Host:   strings.ToLower(parsed.Hostname()),
		Port:   parsed.Port(),
	}
	if parsed.User != nil {
		remote.User = parsed.User.Username()
	}
	if scheme == "file" {
		if remote.Host != "" && remote.Host != "localhost" {
			return Remote{}, fmt.Errorf("file git remote has non-local host")
		}
		remote.Host = ""
		remote.Repository = filepath.Clean(parsed.Path)
		return remote, nil
	}
	if remote.Host == "" || parsed.Path == "" || parsed.Path == "/" {
		return Remote{}, fmt.Errorf("git remote must include host and repository")
	}
	remote.Repository = cleanHostedRepository(parsed.EscapedPath())
	if decoded, decodeErr := url.PathUnescape(remote.Repository); decodeErr == nil {
		remote.Repository = decoded
	}
	return remote, nil
}

func cleanHostedRepository(repository string) string {
	return strings.TrimSuffix(strings.Trim(repository, "/"), ".git")
}

func (r Remote) SourceKey() string {
	switch r.Scheme {
	case "https":
		return "https://" + net.JoinHostPort(r.Host, defaultPort(r.Port, "443"))
	case "ssh":
		user := r.User
		if user == "" {
			user = "git"
		}
		return "ssh://" + user + "@" + net.JoinHostPort(r.Host, defaultPort(r.Port, "22"))
	case "file", "local":
		return "local"
	default:
		return ""
	}
}

func defaultPort(port, fallback string) string {
	if port != "" {
		return port
	}
	return fallback
}

func (r Remote) RepositoryKey() string {
	if r.Host != "" {
		return r.Host + "/" + cleanHostedRepository(r.Repository)
	}
	absolute, err := filepath.Abs(r.Repository)
	if err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(r.Repository)
}

func ResolveRemoteURL(raw, base string) (string, error) {
	remote, err := ParseRemote(raw)
	if err != nil {
		return "", err
	}
	if remote.Scheme != "local" || filepath.IsAbs(remote.Repository) {
		return raw, nil
	}
	absolute, err := filepath.Abs(filepath.Join(base, remote.Repository))
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func (r Remote) Redacted() string {
	return RedactRemote(r.Raw)
}

func RedactRemote(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		parsed.User = url.User("REDACTED")
		return parsed.String()
	}
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return raw
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "REDACTED")
	return parsed.String()
}

func RedactDiagnostic(diagnostic string, remotes ...string) string {
	for _, remote := range remotes {
		diagnostic = strings.ReplaceAll(diagnostic, remote, RedactRemote(remote))
	}
	return credentialURLPattern.ReplaceAllStringFunc(diagnostic, RedactRemote)
}

var credentialURLPattern = regexp.MustCompile(`(?i)(?:https|ssh)://[^\s"'<>]+`)

func (r Remote) SSHCandidate() (string, bool) {
	if r.Scheme != "https" || r.Port != "" || !knownSSHHost(r.Host) || r.Repository == "" {
		return "", false
	}
	return "git@" + r.Host + ":" + cleanHostedRepository(r.Repository) + ".git", true
}

func KnownHostSSHCandidate(raw string) (string, bool) {
	remote, err := ParseRemote(raw)
	if err != nil {
		return "", false
	}
	return remote.SSHCandidate()
}

func knownSSHHost(host string) bool {
	switch strings.ToLower(host) {
	case "github.com", "gitlab.com", "codeberg.org":
		return true
	default:
		return false
	}
}

func ProbeRepository(ctx context.Context, remote string) error {
	cmd := remoteCommand(ctx, "ls-remote", remote)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return commandError(ctx, "git ls-remote "+RedactRemote(remote), RedactDiagnostic(string(out), remote), err)
	}
	return nil
}

func SetRemoteURL(repoPath, remoteURL string) error {
	return SetRemoteURLFor(repoPath, "origin", remoteURL)
}

func SetRemoteURLFor(repoPath, name, remoteURL string) error {
	cmd := exec.Command("git", "-C", repoPath, "config", "--replace-all", "remote."+name+".url", remoteURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote %s in %s: %s", name, repoPath, strings.TrimSpace(RedactDiagnostic(string(out), remoteURL)))
	}
	_ = exec.Command("git", "-C", repoPath, "config", "--unset-all", "remote."+name+".pushurl").Run()
	return nil
}

func ListRemotes(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git remote in %s: %w", repoPath, err)
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes, nil
}

func RemoteURL(repoPath string) (string, error) {
	return RemoteURLFor(repoPath, "origin")
}

func RemoteURLFor(repoPath, name string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", name)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ConfiguredRemoteURL(repoPath, name string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote."+name+".url")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RemoteBindingExact(repoPath, name, expected string) bool {
	urls, err := configuredValues(repoPath, "remote."+name+".url")
	if err != nil || len(urls) != 1 || urls[0] != expected {
		return false
	}
	pushURLs, err := configuredValues(repoPath, "remote."+name+".pushurl")
	if err != nil {
		return true
	}
	return len(pushURLs) == 1 && pushURLs[0] == expected
}

func configuredValues(repoPath, key string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get-all", key)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var values []string
	for _, value := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values, nil
}

func HasRemote(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "remote")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func ParseRepoName(remote string) string {
	parsed, err := ParseRemote(remote)
	if err == nil {
		return filepath.Base(cleanHostedRepository(parsed.Repository))
	}
	remote = strings.TrimSuffix(remote, ".git")
	if index := strings.LastIndex(remote, "/"); index >= 0 {
		return remote[index+1:]
	}
	if index := strings.LastIndex(remote, ":"); index >= 0 {
		return remote[index+1:]
	}
	return remote
}
