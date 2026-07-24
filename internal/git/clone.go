package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/layout"
)

func CloneBare(remote, dest string) error {
	return CloneBareContext(context.Background(), remote, dest)
}

func CloneBareContext(ctx context.Context, remote, dest string) error {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return fmt.Errorf("claim clone destination %s: %w", dest, err)
	}
	err := cloneBareIntoDestinationContext(ctx, remote, dest)
	if err != nil {
		_ = os.RemoveAll(dest)
	}
	return err
}

func cloneBareIntoDestinationContext(ctx context.Context, remote, dest string) error {
	cmd := remoteCommand(ctx, "clone", "--bare", remote, dest)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return commandError(ctx, "git clone --bare "+RedactRemote(remote), RedactDiagnostic(string(out), remote), err)
}

func CloneBareLocal(srcRepoPath, destBarePath string) error {
	return CloneBareLocalContext(context.Background(), srcRepoPath, destBarePath)
}

func CloneBareLocalContext(ctx context.Context, srcRepoPath, destBarePath string) error {
	if err := os.Mkdir(destBarePath, 0o755); err != nil {
		return fmt.Errorf("claim clone destination %s: %w", destBarePath, err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", "--no-local", srcRepoPath, destBarePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(destBarePath)
		return commandError(ctx, "git clone --bare --no-local "+srcRepoPath, string(out), err)
	}
	return nil
}

type CloneOptions struct {
	PromptDefaultBranch func(project string, candidates []string) (string, error)
	Logf                func(format string, args ...interface{})
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
	ErrAlreadyCloned  = errors.New("project already cloned")
	ErrNeedsMigration = errors.New("project exists as plain clone, run 'ws migrate'")
	ErrPathBlocked    = errors.New("non-repo files present at project path")
	ErrNeedsBootstrap = errors.New("default branch needs interactive selection")
)

func CloneIntoLayout(wsRoot, name string, proj *config.Project, opts CloneOptions) (*CloneResult, error) {
	return CloneIntoLayoutContext(context.Background(), wsRoot, name, proj, opts)
}

func CloneIntoLayoutContext(ctx context.Context, wsRoot, name string, proj *config.Project, opts CloneOptions) (_ *CloneResult, resultErr error) {
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
	if err := claimCloneLayout(barePath, mainPath); err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(mainPath)
			_ = os.RemoveAll(barePath)
		}
	}()
	return cloneAndMaterializeContext(ctx, name, proj, opts, barePath, mainPath)
}

func cloneAndMaterializeContext(ctx context.Context, name string, proj *config.Project, opts CloneOptions, barePath, mainPath string) (*CloneResult, error) {
	opts.logf("clone %s: git clone --bare %s → %s", name, RedactRemote(proj.Remote), barePath)
	if err := cloneBareIntoDestinationContext(ctx, proj.Remote, barePath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defaultBranch, err := initBareLayout(name, proj, barePath, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := materializeMainWorktree(ctx, name, barePath, mainPath, defaultBranch, opts); err != nil {
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

func claimCloneLayout(barePath, mainPath string) error {
	if err := os.Mkdir(barePath, 0o755); err != nil {
		if os.IsExist(err) {
			return ErrAlreadyCloned
		}
		return fmt.Errorf("claim bare path %s: %w", barePath, err)
	}
	if err := os.Mkdir(mainPath, 0o755); err != nil {
		_ = os.RemoveAll(barePath)
		if os.IsExist(err) {
			if IsRepo(mainPath) {
				return ErrNeedsMigration
			}
			return ErrPathBlocked
		}
		return fmt.Errorf("claim main path %s: %w", mainPath, err)
	}
	return nil
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

func materializeMainWorktree(ctx context.Context, name, barePath, mainPath, defaultBranch string, opts CloneOptions) error {
	opts.logf("clone %s: worktree add %s on %s", name, mainPath, defaultBranch)
	if err := worktreeAddContext(ctx, barePath, mainPath, defaultBranch, ""); err != nil {
		return fmt.Errorf("worktree add: %w", err)
	}
	if !IsRepo(mainPath) {
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
	for _, candidate := range []string{"main", "master", "trunk"} {
		if HasBranch(barePath, candidate) {
			out = append(out, candidate)
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
