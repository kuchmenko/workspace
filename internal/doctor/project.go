package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

func (r *Runner) projectChecks(name string, proj config.Project) []Finding {
	barePath, layoutFinding := r.checkLayout(name, proj)
	findings := []Finding{layoutFinding}

	if barePath == "" {
		return findings
	}

	findings = append(findings,
		r.checkFetchRefspec(name, barePath),
		r.checkRemoteURL(name, proj, barePath),
	)
	if !r.SkipRemote {
		findings = append(findings, r.checkRemoteReach(name, barePath))
	}
	findings = append(findings,
		r.checkDefaultBranch(name, proj, barePath),
		r.checkBranchUpstream(name, proj, barePath),
	)
	findings = append(findings, r.checkIndexLock(name, barePath)...)
	return findings
}

func (r *Runner) checkLayout(name string, proj config.Project) (string, Finding) {
	mainPath := filepath.Join(r.WsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	bareExists := pathExists(barePath)
	mainExists := pathExists(mainPath)

	switch {
	case bareExists:
		return barePath, Finding{
			Scope:    name,
			Check:    "layout",
			Severity: OK,
			Message:  "bare+worktree layout in place",
		}
	case mainExists && git.IsRepo(mainPath):
		return "", Finding{
			Scope:    name,
			Check:    "layout",
			Severity: Warn,
			Message:  "plain checkout — not migrated to bare+worktree layout",
			FixHint:  fmt.Sprintf("run `ws migrate %s`", name),
		}
	case mainExists:
		return "", Finding{
			Scope:    name,
			Check:    "layout",
			Severity: Error,
			Message:  fmt.Sprintf("path %s exists but is not a git repo", mainPath),
			FixHint:  "move files aside and re-bootstrap, or investigate by hand",
		}
	default:
		return "", Finding{
			Scope:    name,
			Check:    "layout",
			Severity: Warn,
			Message:  "project registered but not cloned on this machine",
			FixHint:  fmt.Sprintf("run `ws bootstrap %s`", name),
		}
	}
}

func (r *Runner) checkFetchRefspec(name, barePath string) Finding {
	if git.HasFetchRefspec(barePath) {
		return Finding{
			Scope:    name,
			Check:    "fetch-refspec",
			Severity: OK,
			Message:  "fetch refspec configured",
		}
	}
	return Finding{
		Scope:    name,
		Check:    "fetch-refspec",
		Severity: Error,
		Message:  "bare repo is missing remote.origin.fetch — fetch won't update origin/* refs",
		FixHint:  "set refspec to +refs/heads/*:refs/remotes/origin/*",
		Fix: func() error {
			return git.SetFetchRefspec(barePath)
		},
	}
}

func (r *Runner) checkRemoteURL(name string, proj config.Project, barePath string) Finding {
	actual, err := git.RemoteURL(barePath)
	if err != nil {
		return Finding{
			Scope:    name,
			Check:    "remote-url",
			Severity: Error,
			Message:  fmt.Sprintf("cannot read origin URL: %v", err),
			FixHint:  "check bare repo integrity",
		}
	}
	if strings.TrimSpace(actual) == strings.TrimSpace(proj.Remote) {
		return Finding{
			Scope:    name,
			Check:    "remote-url",
			Severity: OK,
			Message:  "remote URL matches workspace.toml",
		}
	}
	declared := proj.Remote
	return Finding{
		Scope:    name,
		Check:    "remote-url",
		Severity: Error,
		Message:  fmt.Sprintf("origin URL %q does not match workspace.toml %q", actual, declared),
		FixHint:  "reset origin URL to match workspace.toml",
		Fix: func() error {
			return git.SetRemoteURL(barePath, declared)
		},
	}
}

func (r *Runner) checkRemoteReach(name, barePath string) Finding {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", barePath, "ls-remote", "--exit-code", "origin", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if ctx.Err() == context.DeadlineExceeded {
			msg = "timed out after 10s"
		}
		if msg == "" {
			msg = err.Error()
		}
		return Finding{
			Scope:    name,
			Check:    "remote-reach",
			Severity: Warn,
			Message:  fmt.Sprintf("cannot reach origin: %s", truncate(msg, 120)),
			FixHint:  "check network / SSH key / gh auth status",
		}
	}
	return Finding{
		Scope:    name,
		Check:    "remote-reach",
		Severity: OK,
		Message:  "remote reachable",
	}
}

func (r *Runner) checkDefaultBranch(name string, proj config.Project, barePath string) Finding {
	if strings.TrimSpace(proj.DefaultBranch) != "" {
		return Finding{
			Scope:    name,
			Check:    "default-branch",
			Severity: OK,
			Message:  fmt.Sprintf("default branch: %s", proj.DefaultBranch),
		}
	}
	detected := git.SymbolicRef(barePath, "refs/remotes/origin/HEAD")
	if detected == "" {
		detected = probeFallbackBranch(barePath)
	}
	if detected == "" {
		return Finding{
			Scope:    name,
			Check:    "default-branch",
			Severity: Warn,
			Message:  "default_branch not set in workspace.toml and could not be auto-detected",
			FixHint:  "edit workspace.toml manually",
		}
	}

	if i := strings.Index(detected, "/"); i >= 0 {
		detected = detected[i+1:]
	}
	wsRoot := r.WsRoot
	ws := r.WS
	return Finding{
		Scope:    name,
		Check:    "default-branch",
		Severity: Warn,
		Message:  fmt.Sprintf("default_branch missing in workspace.toml (detected %q from bare)", detected),
		FixHint:  fmt.Sprintf("persist %q as default_branch", detected),
		Fix: func() error {
			p := ws.Projects[name]
			p.DefaultBranch = detected
			ws.Projects[name] = p
			return config.Save(wsRoot, ws)
		},
	}
}

func probeFallbackBranch(barePath string) string {
	for _, b := range []string{"main", "master"} {
		if git.HasBranch(barePath, b) {
			return b
		}
	}
	return ""
}

func (r *Runner) checkBranchUpstream(name string, proj config.Project, barePath string) Finding {
	branch := strings.TrimSpace(proj.DefaultBranch)
	if branch == "" {
		return Finding{
			Scope:    name,
			Check:    "branch-upstream",
			Severity: OK,
			Message:  "skipped (default_branch not set)",
		}
	}
	if !git.HasBranch(barePath, branch) {
		return Finding{
			Scope:    name,
			Check:    "branch-upstream",
			Severity: Warn,
			Message:  fmt.Sprintf("default branch %q not present locally — nothing to configure", branch),
			FixHint:  "fetch from origin or verify default_branch",
		}
	}
	if git.HasUpstream(barePath, branch) {
		return Finding{
			Scope:    name,
			Check:    "branch-upstream",
			Severity: OK,
			Message:  fmt.Sprintf("branch %q tracks origin", branch),
		}
	}
	skipRemote := r.SkipRemote
	return Finding{
		Scope:    name,
		Check:    "branch-upstream",
		Severity: Warn,
		Message:  fmt.Sprintf("branch %q has no upstream — plain `git push`/`git pull` will fail", branch),
		FixHint:  fmt.Sprintf("set branch.%s.remote=origin and branch.%s.merge=refs/heads/%s", branch, branch, branch),
		Fix: func() error {
			if err := git.SetBranchUpstream(barePath, branch, "origin"); err != nil {
				return err
			}
			if skipRemote {
				return nil
			}

			_ = git.Fetch(barePath)
			return nil
		},
	}
}

func (r *Runner) checkIndexLock(name, barePath string) []Finding {
	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return []Finding{{
			Scope:    name,
			Check:    "index-lock",
			Severity: Warn,
			Message:  fmt.Sprintf("cannot enumerate worktrees: %v", err),
		}}
	}
	locked := lockedWorktrees(wts)
	if len(locked) == 0 {
		return []Finding{{
			Scope:    name,
			Check:    "index-lock",
			Severity: OK,
			Message:  "no stale index locks",
		}}
	}
	out := make([]Finding, 0, len(locked))
	for _, p := range locked {
		out = append(out, Finding{
			Scope:    name,
			Check:    "index-lock",
			Severity: Warn,
			Message:  fmt.Sprintf("index.lock present at %s", p),
			FixHint:  "verify no git process is running there, then remove .git/index.lock by hand",
		})
	}
	return out
}

func lockedWorktrees(wts []git.Worktree) []string {
	var out []string
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		if git.HasIndexLock(wt.Path) {
			out = append(out, wt.Path)
		}
	}
	return out
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
