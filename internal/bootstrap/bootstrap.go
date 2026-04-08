package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// State classifies how a project looks on disk relative to wsRoot. Mirrors
// (but is intentionally separate from) migrate.Check — bootstrap cares about
// "do I clone this?" while migrate cares about "do I convert this?".
type State string

const (
	// StatePresent means <path>.bare exists; the project is fully cloned.
	StatePresent State = "present"
	// StateNeedsMigrate means <path> exists as a plain git checkout. Bootstrap
	// won't touch it; the user must run `ws migrate`.
	StateNeedsMigrate State = "needs-migrate"
	// StateBlocked means <path> exists but isn't a git repo at all. Bootstrap
	// won't clobber it; the user must clean up by hand.
	StateBlocked State = "blocked"
	// StateSelf means proj.Remote points at the same repo that hosts
	// workspace.toml itself. Bootstrap skips it to avoid cloning the workspace
	// inside its own tree.
	StateSelf State = "self"
	// StateMissing means nothing exists at <path>; bootstrap will clone here.
	StateMissing State = "missing"
)

// PlanItem is one row in the bootstrap plan.
type PlanItem struct {
	Name    string
	Project config.Project
	State   State
	// Reason carries a human-readable explanation for non-clonable states.
	Reason string
}

// Plan is the result of scanning workspace.toml against the local filesystem.
// It contains every active project, classified into one bucket each. Use the
// Bucket helpers to display them in TUI sections.
type Plan struct {
	Items []PlanItem
}

// Bucket returns the items matching the given state, in stable name order.
func (p *Plan) Bucket(s State) []PlanItem {
	var out []PlanItem
	for _, it := range p.Items {
		if it.State == s {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ToClone is the list of project names that bootstrap will actually clone.
func (p *Plan) ToClone() []string {
	items := p.Bucket(StateMissing)
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}

// ScanPlan walks ws.Projects, classifies each active project, and returns the
// bootstrap plan. Filtering by `only` (when non-empty) restricts the scan to
// the named projects — used by `ws bootstrap <name>`.
func ScanPlan(wsRoot string, ws *config.Workspace, only []string) *Plan {
	wantOnly := map[string]bool{}
	for _, n := range only {
		wantOnly[n] = true
	}

	selfRemote := workspaceSelfRemote(wsRoot)

	plan := &Plan{}
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		if len(wantOnly) > 0 && !wantOnly[name] {
			continue
		}
		item := PlanItem{Name: name, Project: proj}
		item.State, item.Reason = classify(wsRoot, proj, selfRemote)
		plan.Items = append(plan.Items, item)
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].Name < plan.Items[j].Name })
	return plan
}

// classify is the per-project state machine. Order matters:
//
//  1. self-detection (don't clone the workspace into itself)
//  2. <path>.bare exists → present (already cloned)
//  3. <path> is a git repo → needs-migrate
//  4. <path> exists but not a repo → blocked
//  5. nothing → missing (clone candidate)
func classify(wsRoot string, proj config.Project, selfRemote string) (State, string) {
	if selfRemote != "" && remotesEqual(proj.Remote, selfRemote) {
		return StateSelf, "this is the workspace repository itself"
	}
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	if statExists(barePath) {
		return StatePresent, ""
	}
	if statExists(mainPath) {
		if git.IsRepo(mainPath) {
			return StateNeedsMigrate, "plain checkout — run `ws migrate " + proj.Path + "`"
		}
		return StateBlocked, "non-repo files at " + mainPath
	}
	return StateMissing, ""
}

// statExists treats any stat error (including permission denied) as
// "not present" — bootstrap is read-only at this stage.
func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// workspaceSelfRemote returns the origin URL of the git repo that contains
// workspace.toml, or "" if workspace.toml is not in a git repo. Used for
// self-detection so we don't clone the workspace inside itself.
func workspaceSelfRemote(wsRoot string) string {
	root := findRepoRoot(wsRoot)
	if root == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", root, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// findRepoRoot walks up from dir looking for the nearest git repo. Mirrors
// the helper in internal/daemon/reconciler.go (kept duplicated rather than
// exported because the daemon's helper is private and tightly scoped).
func findRepoRoot(dir string) string {
	for {
		if git.IsRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// remotesEqual normalizes two git remote URLs and compares them.
//
// Equivalences considered equal:
//
//	git@github.com:foo/bar.git   ≡   https://github.com/foo/bar.git
//	git@github.com:foo/bar.git   ≡   git@github.com:foo/bar
//	https://github.com/foo/bar/  ≡   https://github.com/foo/bar
func remotesEqual(a, b string) bool {
	return normalizeRemote(a) == normalizeRemote(b)
}

func normalizeRemote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// strip ssh prefix: git@host:path → host/path
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		s = strings.Replace(s, ":", "/", 1)
	}
	// strip protocol
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		s = strings.TrimPrefix(s, p)
	}
	// strip trailing .git and slashes
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.ToLower(s)
}
