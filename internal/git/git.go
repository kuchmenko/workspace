package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/layout"
)

func CloneBare(remote, dest string) error {
	cmd := exec.Command("git", "clone", "--bare", remote, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

func CloneBareLocal(srcRepoPath, destBarePath string) error {
	cmd := exec.Command("git", "clone", "--bare", "--no-local", srcRepoPath, destBarePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare --no-local %s: %s", srcRepoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func IsBare(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func SetRemoteURL(repoPath, url string) error {
	return SetRemoteURLFor(repoPath, "origin", url)
}

func SetRemoteURLFor(repoPath, name, url string) error {
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-url", name, url)
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("git", "-C", repoPath, "remote", "add", name, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote %s in %s: %s", name, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func ListRemotes(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "remote")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git remote in %s: %w", repoPath, err)
	}
	var remotes []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			remotes = append(remotes, l)
		}
	}
	return remotes, nil
}

// EnsureMirrorRemote installs (or repairs) a push-only mirror remote on a
// bare repo. skipFetchAll keeps `git fetch --all` from pulling the mirror's
// refs back in — the mirror only ever receives pushes.
func EnsureMirrorRemote(repoPath, name, url string) error {
	if name == "" || name == "origin" {
		return fmt.Errorf("mirror remote name %q is reserved", name)
	}
	if err := SetRemoteURLFor(repoPath, name, url); err != nil {
		return err
	}
	return setConfig(repoPath, "remote."+name+".skipFetchAll", "true")
}

func MirrorRemoteOK(repoPath, name, url string) bool {
	got, err := RemoteURLFor(repoPath, name)
	if err != nil || got != url {
		return false
	}
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote."+name+".skipFetchAll")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// PushMirror pushes everything origin has — branches and tags — to the named
// mirror remote. Branch refs are enumerated explicitly: negative refspecs
// only apply to fetch, so a glob push would turn the origin/HEAD symref into
// a literal "HEAD" branch on the mirror. No --force: a diverged mirror fails
// the push and surfaces as a conflict.
func PushMirror(repoPath, name string) error {
	cmd := exec.Command("git", "-C", repoPath, "for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git for-each-ref in %s: %w", repoPath, err)
	}
	refspecs := []string{"refs/tags/*:refs/tags/*"}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" || ref == "refs/remotes/origin/HEAD" {
			continue
		}
		branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
		refspecs = append(refspecs, ref+":refs/heads/"+branch)
	}
	args := append([]string{"-C", repoPath, "push", name}, refspecs...)
	pushOut, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s in %s: %s", name, repoPath, strings.TrimSpace(string(pushOut)))
	}
	return nil
}

func SetRemoteHead(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-head", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote head %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func FetchRefspec(repoPath, source, refspec string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", source, refspec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s %s in %s: %s", source, refspec, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

const standardFetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

func SetFetchRefspec(repoPath string) error {
	return setConfig(repoPath, "remote.origin.fetch", standardFetchRefspec)
}

func HasFetchRefspec(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get-all", "remote.origin.fetch")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func HasBranch(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func HasRemoteBranch(repoPath, remote, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return cmd.Run() == nil
}

type CloneOptions struct {
	PromptDefaultBranch func(project string, candidates []string) (string, error)

	Logf func(format string, args ...interface{})
}

func (o CloneOptions) logf(format string, args ...interface{}) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

type CloneResult struct {
	Project       string
	BarePath      string
	MainWorktree  string
	DefaultBranch string
}

var (
	ErrAlreadyCloned = errors.New("project already cloned")

	ErrNeedsMigration = errors.New("project exists as plain clone, run 'ws migrate'")

	ErrPathBlocked = errors.New("non-repo files present at project path")

	ErrNeedsBootstrap = errors.New("default branch needs interactive selection")
)

func CloneIntoLayout(wsRoot, name string, proj *config.Project, opts CloneOptions) (*CloneResult, error) {
	if err := validateCloneInputs(name, proj); err != nil {
		return nil, err
	}
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)
	if err := preflightLayout(barePath, mainPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(barePath), 0o755); err != nil {
		return nil, fmt.Errorf("create parent %s: %w", filepath.Dir(barePath), err)
	}
	opts.logf("clone %s: git clone --bare %s → %s", name, proj.Remote, barePath)
	if err := CloneBare(proj.Remote, barePath); err != nil {
		return nil, err
	}
	defaultBranch, err := initBareLayout(name, proj, barePath, opts)
	if err != nil {
		_ = os.RemoveAll(barePath)
		return nil, err
	}
	if err := materializeMainWorktree(name, barePath, mainPath, defaultBranch, opts); err != nil {
		return nil, err
	}
	proj.DefaultBranch = defaultBranch
	return &CloneResult{
		Project:       name,
		BarePath:      barePath,
		MainWorktree:  mainPath,
		DefaultBranch: defaultBranch,
	}, nil
}

func validateCloneInputs(name string, proj *config.Project) error {
	if proj == nil {
		return fmt.Errorf("clone %s: nil project", name)
	}
	if proj.Remote == "" {
		return fmt.Errorf("clone %s: empty remote", name)
	}
	if proj.Path == "" {
		return fmt.Errorf("clone %s: empty path", name)
	}
	return nil
}

func preflightLayout(barePath, mainPath string) error {
	if _, err := os.Stat(barePath); err == nil {
		return ErrAlreadyCloned
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", barePath, err)
	}
	info, err := os.Stat(mainPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", mainPath, err)
	}
	if info.IsDir() && IsRepo(mainPath) {
		return ErrNeedsMigration
	}
	return ErrPathBlocked
}

func initBareLayout(name string, proj *config.Project, barePath string, opts CloneOptions) (string, error) {
	if err := SetFetchRefspec(barePath); err != nil {
		return "", fmt.Errorf("set fetch refspec: %w", err)
	}
	for mirror, url := range proj.Mirrors {
		if err := EnsureMirrorRemote(barePath, mirror, url); err != nil {
			opts.logf("clone %s: warning: mirror remote %s: %v", name, mirror, err)
		}
	}
	defaultBranch, err := resolveDefaultBranch(name, proj, barePath, opts)
	if err != nil {
		return "", err
	}
	opts.logf("clone %s: default branch = %s", name, defaultBranch)

	_ = SetRemoteHead(barePath, defaultBranch)
	return defaultBranch, nil
}

func materializeMainWorktree(name, barePath, mainPath, defaultBranch string, opts CloneOptions) error {
	opts.logf("clone %s: worktree add %s on %s", name, mainPath, defaultBranch)
	if err := WorktreeAdd(barePath, mainPath, defaultBranch, ""); err != nil {
		_ = os.RemoveAll(barePath)
		_ = os.RemoveAll(mainPath)
		return fmt.Errorf("worktree add: %w", err)
	}
	if !IsRepo(mainPath) {
		_ = os.RemoveAll(mainPath)
		_ = os.RemoveAll(barePath)
		return fmt.Errorf("verification failed: %s is not a git repo after worktree add", mainPath)
	}

	if err := SetBranchUpstream(barePath, defaultBranch, "origin"); err != nil {
		opts.logf("clone %s: warning: could not set upstream for %s: %v", name, defaultBranch, err)
	}
	return nil
}

func resolveDefaultBranch(name string, proj *config.Project, barePath string, opts CloneOptions) (string, error) {
	if proj.DefaultBranch != "" {
		return proj.DefaultBranch, nil
	}
	if br := defaultBranchFromOriginHEAD(barePath); br != "" {
		return br, nil
	}
	candidates := wellKnownDefaultCandidates(barePath)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if opts.PromptDefaultBranch == nil {
		return "", ErrNeedsBootstrap
	}
	return promptForDefaultBranch(name, candidates, opts.PromptDefaultBranch)
}

func defaultBranchFromOriginHEAD(barePath string) string {
	br := SymbolicRef(barePath, "refs/remotes/origin/HEAD")
	if br == "" {
		return ""
	}
	if i := strings.Index(br, "/"); i >= 0 {
		return br[i+1:]
	}
	return br
}

func wellKnownDefaultCandidates(barePath string) []string {
	var out []string
	for _, c := range []string{"main", "master", "trunk"} {
		if HasBranch(barePath, c) {
			out = append(out, c)
		}
	}
	return out
}

func promptForDefaultBranch(name string, candidates []string, prompt func(string, []string) (string, error)) (string, error) {
	picked, err := prompt(name, candidates)
	if err != nil {
		return "", err
	}
	picked = strings.TrimSpace(picked)
	if picked == "" {
		return "", fmt.Errorf("no default branch selected for %s", name)
	}
	return picked, nil
}

func Pull(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func IsRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	return cmd.Run() == nil
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

func CurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func Branches(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var branches []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			branches = append(branches, l)
		}
	}
	return branches, nil
}

func LastCommitTime(repoPath string) (time.Time, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%cI")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

func LastCommitAuthorTime(repoPath string) (time.Time, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%aI")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

func LastCommitMessage(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func HasRemote(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "remote")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func Add(repoPath, file string) error {
	cmd := exec.Command("git", "-C", repoPath, "add", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Commit(repoPath, message string) error {
	cmd := exec.Command("git", "-C", repoPath, "commit", "-m", message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Push(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "push")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func PushBranch(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "push", "--set-upstream", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Fetch(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--all", "--prune", "--tags")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func RevParse(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func AheadBehind(repoPath, branch string) (ahead, behind int, hasUpstream bool) {
	upstream := branch + "@{u}"
	if RevParse(repoPath, upstream) == "" {
		return 0, 0, false
	}
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--left-right", "--count", upstream+"..."+branch)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, false
	}
	fmt.Sscanf(parts[0], "%d", &behind)
	fmt.Sscanf(parts[1], "%d", &ahead)
	return ahead, behind, true
}

func IsDirty(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

func HasIndexLock(repoPath string) bool {
	gitDir := RevParse(repoPath, "--git-dir")
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}
	_, err := os.Stat(filepath.Join(gitDir, "index.lock"))
	return err == nil
}

func HasUpstream(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	return cmd.Run() == nil
}

func SetBranchUpstream(repoPath, branch, remote string) error {
	if branch == "" || remote == "" {
		return fmt.Errorf("SetBranchUpstream: empty branch or remote")
	}
	if err := setConfig(repoPath, "branch."+branch+".remote", remote); err != nil {
		return err
	}
	return setConfig(repoPath, "branch."+branch+".merge", "refs/heads/"+branch)
}

func setConfig(repoPath, key, value string) error {
	cmd := exec.Command("git", "-C", repoPath, "config", key, value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config %s=%s in %s: %s", key, value, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func HasStash(repoPath string) bool {
	return StashCount(repoPath) > 0
}

func StashCount(repoPath string) int {
	cmd := exec.Command("git", "-C", repoPath, "stash", "list")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func SymbolicRef(repoPath, ref string) string {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ParseRepoName(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if idx := strings.LastIndex(remote, "/"); idx >= 0 {
		return remote[idx+1:]
	}
	if idx := strings.LastIndex(remote, ":"); idx >= 0 {
		return remote[idx+1:]
	}
	return remote
}

type Worktree struct {
	Path     string
	HEAD     string
	Branch   string
	Bare     bool
	Detached bool
}

func WorktreeAdd(repoPath, wtPath, branch, createFromBase string) error {
	args := []string{"-C", repoPath, "worktree", "add"}
	if createFromBase != "" {
		args = append(args, "-b", branch, wtPath, createFromBase)
	} else {
		args = append(args, wtPath, branch)
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeAddNoCheckout(repoPath, wtPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "--no-checkout", wtPath, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add --no-checkout %s in %s: %s", wtPath, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeRepair(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "repair")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree repair in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeRemove(repoPath, wtPath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove %s: %s", wtPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func WorktreeList(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w", repoPath, err)
	}
	return parsePorcelainWorktreeList(string(out)), nil
}

func parsePorcelainWorktreeList(text string) []Worktree {
	var (
		result []Worktree
		cur    Worktree
		open   bool
	)
	flush := func() {
		if open {
			result = append(result, cur)
		}
		cur = Worktree{}
		open = false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		open = true
		applyWorktreeLine(&cur, line)
	}
	flush()
	return result
}

func applyWorktreeLine(cur *Worktree, line string) {
	switch {
	case strings.HasPrefix(line, "worktree "):
		cur.Path = strings.TrimPrefix(line, "worktree ")
	case strings.HasPrefix(line, "HEAD "):
		cur.HEAD = strings.TrimPrefix(line, "HEAD ")
	case strings.HasPrefix(line, "branch "):
		ref := strings.TrimPrefix(line, "branch ")
		cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
	case line == "bare":
		cur.Bare = true
	case line == "detached":
		cur.Detached = true
	}
}
