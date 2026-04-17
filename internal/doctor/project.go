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

// projectChecks runs every per-project check for one active project.
// Order mirrors the natural "does it exist → does its git state look
// right → does its branch config look right" progression.
//
// Checks that don't make sense for the current layout (e.g. fetch-refspec
// on a plain checkout) short-circuit inside layoutOnlyInspectable so the
// report doesn't pile up "not applicable" findings.
func (r *Runner) projectChecks(name string, proj config.Project) []Finding {
	barePath, layoutFinding := r.checkLayout(name, proj)
	findings := []Finding{layoutFinding}

	// If the bare repo isn't present we cannot inspect any git state. Skip
	// the rest — running them would raise a flood of secondary errors that
	// are just symptoms of the layout problem.
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

// checkLayout uses the bootstrap package's state machine to classify the
// project. Returns the absolute bare path when the project is present
// (so downstream checks can reuse it), or "" otherwise.
func (r *Runner) checkLayout(name string, proj config.Project) (string, Finding) {
	mainPath := filepath.Join(r.WsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	// Mirror bootstrap.classify without calling it directly — classify is
	// unexported in the bootstrap package, and duplicating the four-line
	// decision here avoids exposing the helper for one caller. We
	// intentionally don't replicate the "self" detection: workspaces
	// can't register themselves as projects in practice, and even if
	// they could, the bare-present branch below handles it correctly.
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

// checkFetchRefspec verifies remote.origin.fetch is configured in the
// bare. This is the #14/PR#16 bug: without the refspec, `git fetch` in a
// bare only updates FETCH_HEAD, so @{u} cannot resolve and AheadBehind
// silently returns (0, 0, false) for every branch. Auto-fix reuses the
// helper from internal/git and additionally runs a fetch so that
// refs/remotes/origin/* actually get populated — setting the refspec
// alone changes config but leaves the tracking refs empty, which breaks
// downstream checks (branch-upstream in particular) even after the fix
// "succeeds".
//
// The fetch is skipped when Runner.SkipRemote is set and treated as
// best-effort otherwise: if it fails (network/auth), the primary goal
// — a correct config value — is still achieved and the error is
// surfaced through checkRemoteReach / the next tick.
func (r *Runner) checkFetchRefspec(name, barePath string) Finding {
	if git.HasFetchRefspec(barePath) {
		return Finding{
			Scope:    name,
			Check:    "fetch-refspec",
			Severity: OK,
			Message:  "fetch refspec configured",
		}
	}
	skipRemote := r.SkipRemote
	return Finding{
		Scope:    name,
		Check:    "fetch-refspec",
		Severity: Error,
		Message:  "bare repo is missing remote.origin.fetch — fetch won't update origin/* refs",
		FixHint:  "set refspec to +refs/heads/*:refs/remotes/origin/*",
		Fix: func() error {
			if err := git.SetFetchRefspec(barePath); err != nil {
				return err
			}
			if skipRemote {
				return nil
			}
			// Populate refs/remotes/origin/* so subsequent checks (and
			// the branch-upstream fix that runs after this one) can
			// resolve against live tracking refs. Best-effort: a
			// network failure here does not invalidate the refspec
			// write we already performed.
			_ = git.Fetch(barePath)
			return nil
		},
	}
}

// checkRemoteURL compares the bare's origin URL against workspace.toml's
// declared remote. They should match exactly — `ws add` / migrate write
// them both in lockstep, and any drift means someone edited one but not
// the other.
//
// We do not attempt normalisation (SSH vs HTTPS, trailing .git) on
// purpose: a mismatch here is almost always a typo in workspace.toml,
// and silently treating "git@github.com:a/b" ≡ "https://github.com/a/b"
// would hide the fact that the two refer to different transports (and
// therefore different credentials / access paths).
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

// checkRemoteReach runs `git ls-remote --exit-code origin HEAD` with a
// short timeout. A failure here can be network-level (offline, DNS) or
// auth-level (bad SSH key, token expired), but in either case the user
// must fix it — doctor has no business trying to renegotiate credentials.
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

// checkDefaultBranch surfaces projects whose workspace.toml entry is
// missing default_branch. That field is the anchor for ff-pulls, `ws
// worktree new` base resolution, and bootstrap; leaving it empty forces
// every consumer to guess.
//
// The fix is mostly mechanical: `refs/remotes/origin/HEAD` usually
// points at the right branch after a recent fetch. If it doesn't
// resolve, we fall back to "main" / "master" probe — same order git's
// own ls-remote uses. If nothing resolves, we emit the warning without
// an auto-fix so the user picks manually.
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
	// SymbolicRef returns "origin/main"; strip the "origin/" prefix.
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

// probeFallbackBranch checks the usual suspects when refs/remotes/origin/HEAD
// isn't configured (which is the common case for bares cloned before PR#16).
func probeFallbackBranch(barePath string) string {
	for _, b := range []string{"main", "master"} {
		if git.HasBranch(barePath, b) {
			return b
		}
	}
	return ""
}

// checkBranchUpstream ensures the default branch has branch.<X>.remote
// and branch.<X>.merge configured. Without these, plain `git push` and
// `git pull` in any worktree fail because git can't figure out what to
// talk to. SetBranchUpstream is idempotent and cheap.
func (r *Runner) checkBranchUpstream(name string, proj config.Project, barePath string) Finding {
	branch := strings.TrimSpace(proj.DefaultBranch)
	if branch == "" {
		// Already covered by the default-branch check; don't double-report.
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
	return Finding{
		Scope:    name,
		Check:    "branch-upstream",
		Severity: Warn,
		Message:  fmt.Sprintf("branch %q has no upstream — plain `git push`/`git pull` will fail", branch),
		FixHint:  fmt.Sprintf("set branch.%s.remote=origin and branch.%s.merge=refs/heads/%s", branch, branch, branch),
		Fix: func() error {
			return git.SetBranchUpstream(barePath, branch, "origin")
		},
	}
}

// checkIndexLock walks every worktree under this project and reports any
// that carry a stale .git/index.lock. Removing the lock is risky (could
// corrupt an ongoing git operation), so there is no auto-fix — doctor
// just surfaces them so the user can investigate.
//
// Returns a slice so a multi-worktree project with multiple locks emits
// one finding per worktree rather than concatenating messages.
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
	var out []Finding
	var locked []string
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		if git.HasIndexLock(wt.Path) {
			locked = append(locked, wt.Path)
		}
	}
	if len(locked) == 0 {
		out = append(out, Finding{
			Scope:    name,
			Check:    "index-lock",
			Severity: OK,
			Message:  "no stale index locks",
		})
		return out
	}
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
