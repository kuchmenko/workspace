package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

type Severity int

const (
	OK Severity = iota

	Info

	Warn

	Error
)

func (s Severity) String() string {
	switch s {
	case OK:
		return "ok"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "unknown"
}

func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *Severity) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch raw {
	case "ok":
		*s = OK
	case "info":
		*s = Info
	case "warn":
		*s = Warn
	case "error":
		*s = Error
	default:
		return fmt.Errorf("doctor: unknown severity %q", raw)
	}
	return nil
}

type Finding struct {
	Scope string `json:"scope"`

	Check string `json:"check"`

	Severity Severity `json:"severity"`

	Message string `json:"message"`

	FixHint string `json:"fix_hint,omitempty"`

	Fixed bool `json:"fixed,omitempty"`

	FixError string `json:"fix_error,omitempty"`

	Fix func() error `json:"-"`
}

type Report struct {
	Findings []Finding `json:"findings"`
}

func (r *Report) MaxSeverity() Severity {
	m := OK
	for _, f := range r.Findings {
		if f.Severity > m {
			m = f.Severity
		}
	}
	return m
}

func (r *Report) AutoFixable() []*Finding {
	var out []*Finding
	for i := range r.Findings {
		if r.Findings[i].Fix != nil {
			out = append(out, &r.Findings[i])
		}
	}
	return out
}

type Runner struct {
	WsRoot string

	WS *config.Workspace

	Only string

	SkipRemote bool

	OnScope func(scope string, findings []Finding)
}

func (r *Runner) Run() *Report {
	rep := &Report{}
	emit := func(scope string, findings []Finding) {
		rep.Findings = append(rep.Findings, findings...)
		if r.OnScope != nil {
			r.OnScope(scope, findings)
		}
	}

	emit("system", r.systemChecks())
	for _, name := range r.projectNames() {
		proj := r.WS.Projects[name]
		emit(name, r.projectChecks(name, proj))
	}
	return rep
}

func (r *Runner) projectNames() []string {
	var names []string
	for name, p := range r.WS.Projects {
		if p.Status != config.StatusActive {
			continue
		}
		if r.Only != "" && name != r.Only {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ApplyFixes(rep *Report) int {
	fixed := 0
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if f.Fix == nil {
			continue
		}
		if err := f.Fix(); err != nil {
			f.FixError = err.Error()
			continue
		}
		f.Fixed = true
		fixed++
	}
	return fixed
}

var severitySymbol = map[Severity]string{
	OK:    "✓",
	Info:  "ℹ",
	Warn:  "⚠",
	Error: "✗",
}

func WriteText(w io.Writer, rep *Report) {
	groups := groupByScope(rep.Findings)
	fixable := fixableCount(rep.Findings)

	if findings, ok := groups["system"]; ok {
		writeBlock(w, "System", findings)
		delete(groups, "system")
	}

	for _, scope := range scopeOrder(rep.Findings) {
		if scope == "system" {
			continue
		}
		findings, ok := groups[scope]
		if !ok {
			continue
		}
		fmt.Fprintln(w)
		writeBlock(w, scope, findings)
	}

	WriteFooter(w, rep, fixable)
}

func WriteScope(w io.Writer, scope string, findings []Finding, leading bool) {
	if !leading {
		fmt.Fprintln(w)
	}
	writeBlock(w, scopeTitle(scope), findings)
}

func FixableCount(rep *Report) int {
	return fixableCount(rep.Findings)
}

func fixableCount(findings []Finding) int {
	n := 0
	for _, f := range findings {
		if f.Fix != nil {
			n++
		}
	}
	return n
}

func scopeTitle(scope string) string {
	if scope == "system" {
		return "System"
	}
	return scope
}

func writeBlock(w io.Writer, title string, findings []Finding) {
	fmt.Fprintln(w, title)
	for _, f := range findings {
		sym := severitySymbol[f.Severity]
		fmt.Fprintf(w, "  %s %s: %s\n", sym, f.Check, f.Message)
		switch {
		case f.Fixed:
			fmt.Fprintf(w, "    → fix applied\n")
		case f.FixError != "":
			fmt.Fprintf(w, "    → fix failed: %s\n", f.FixError)
		case f.Fix != nil:
			fmt.Fprintf(w, "    → auto-fixable: %s\n", nonEmpty(f.FixHint, "run with --fix"))
		case f.FixHint != "":
			fmt.Fprintf(w, "    → %s\n", f.FixHint)
		}
	}
}

func WriteFooter(w io.Writer, rep *Report, fixable int) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("━", 21))
	stats := footerStats(rep)
	if stats.fixesApplied > 0 || stats.fixesFailed > 0 {
		writeFixSummary(w, stats)
		return
	}
	writeIssueSummary(w, stats.issues, fixable)
}

type footerCounts struct {
	issues       int
	fixesApplied int
	fixesFailed  int
}

func footerStats(rep *Report) footerCounts {
	var c footerCounts
	for _, f := range rep.Findings {
		if f.Severity >= Warn {
			c.issues++
		}
		if f.Fixed {
			c.fixesApplied++
		}
		if f.FixError != "" {
			c.fixesFailed++
		}
	}
	return c
}

func writeFixSummary(w io.Writer, stats footerCounts) {
	if stats.fixesApplied > 0 {
		fmt.Fprintf(w, "Applied %d fix(es).\n", stats.fixesApplied)
	}
	if stats.fixesFailed > 0 {
		fmt.Fprintf(w, "%d fix(es) failed — see messages above.\n", stats.fixesFailed)
	}
}

func writeIssueSummary(w io.Writer, issues, fixable int) {
	switch {
	case issues == 0:
		fmt.Fprintln(w, "All checks passed.")
	case fixable > 0:
		fmt.Fprintf(w, "%d issue(s) found (%d auto-fixable)\n", issues, fixable)
		fmt.Fprintln(w, "Run `ws doctor --fix` to apply safe fixes.")
	default:
		fmt.Fprintf(w, "%d issue(s) found.\n", issues)
	}
}

func WriteJSON(w io.Writer, rep *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(rep)
}

func groupByScope(findings []Finding) map[string][]Finding {
	out := map[string][]Finding{}
	for _, f := range findings {
		out[f.Scope] = append(out[f.Scope], f)
	}
	return out
}

func scopeOrder(findings []Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range findings {
		if seen[f.Scope] {
			continue
		}
		seen[f.Scope] = true
		out = append(out, f.Scope)
	}
	return out
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

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

func (r *Runner) systemChecks() []Finding {
	return []Finding{
		checkDaemon(),
		checkStaleSidecars(r.WsRoot),
		checkConflicts(r.WsRoot),
		checkConfig(r.WS),
	}
}

func checkDaemon() Finding {
	pid, alive := daemon.IsRunning()
	if alive {
		return Finding{
			Scope:    "system",
			Check:    "daemon",
			Severity: OK,
			Message:  fmt.Sprintf("daemon running (pid %d)", pid),
		}
	}
	return Finding{
		Scope:    "system",
		Check:    "daemon",
		Severity: Warn,
		Message:  "daemon not running — projects won't auto-sync",
		FixHint:  "run `ws daemon start`",
	}
}

func checkStaleSidecars(wsRoot string) Finding {
	stale := findStaleSidecars(wsRoot)
	if len(stale) == 0 {
		return Finding{
			Scope:    "system",
			Check:    "sidecar",
			Severity: OK,
			Message:  "no stale sidecars",
		}
	}
	return Finding{
		Scope:    "system",
		Check:    "sidecar",
		Severity: Warn,
		Message:  fmt.Sprintf("stale sidecar(s) blocking daemon: %v", sidecarKindNames(stale)),
		FixHint:  "remove stale sidecar file(s)",
		Fix:      func() error { return deleteSidecars(wsRoot, stale) },
	}
}

func findStaleSidecars(wsRoot string) []sidecar.Kind {
	kinds := []sidecar.Kind{sidecar.KindBootstrap, sidecar.KindMigrate}
	var stale []sidecar.Kind
	for _, k := range kinds {
		sc, err := sidecar.Load(wsRoot, k)
		if err != nil || sc == nil {
			continue
		}
		if !sidecar.IsAlive(sc) {
			stale = append(stale, k)
		}
	}
	return stale
}

func sidecarKindNames(kinds []sidecar.Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

func deleteSidecars(wsRoot string, kinds []sidecar.Kind) error {
	for _, k := range kinds {
		if err := sidecar.Delete(wsRoot, k); err != nil {
			return fmt.Errorf("delete %s sidecar: %w", k, err)
		}
	}
	return nil
}

func checkConflicts(wsRoot string) Finding {
	mine, err := loadProjectConflicts(wsRoot)
	if err != nil {
		return Finding{
			Scope:    "system",
			Check:    "conflicts",
			Severity: Warn,
			Message:  err.Error(),
		}
	}
	if len(mine) == 0 {
		return Finding{
			Scope:    "system",
			Check:    "conflicts",
			Severity: OK,
			Message:  "no active conflicts",
		}
	}
	oldest := oldestConflict(mine)
	msg := fmt.Sprintf("%d active conflict(s); oldest: %s (%s, %s ago)",
		len(mine),
		oldest.Kind,
		projectOrGlobal(oldest),
		humanizeAge(time.Since(oldest.DetectedAt)),
	)
	return Finding{
		Scope:    "system",
		Check:    "conflicts",
		Severity: Error,
		Message:  msg,
		FixHint:  "run `ws sync resolve`",
	}
}

func loadProjectConflicts(wsRoot string) ([]daemon.Conflict, error) {
	store, err := daemon.OpenConflictStore()
	if err != nil {
		return nil, fmt.Errorf("cannot read conflict store: %w", err)
	}
	all, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("cannot list conflicts: %w", err)
	}
	absWsRoot, _ := filepath.Abs(wsRoot)
	var mine []daemon.Conflict
	for _, c := range all {
		abs, _ := filepath.Abs(c.Workspace)
		if abs == absWsRoot {
			mine = append(mine, c)
		}
	}
	return mine, nil
}

func oldestConflict(conflicts []daemon.Conflict) daemon.Conflict {
	oldest := conflicts[0]
	for _, c := range conflicts[1:] {
		if c.DetectedAt.Before(oldest.DetectedAt) {
			oldest = c
		}
	}
	return oldest
}

func projectOrGlobal(c daemon.Conflict) string {
	if c.Project != "" {
		return c.Project
	}
	return "workspace"
}

func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func checkConfig(ws *config.Workspace) Finding {
	if ws == nil {
		return Finding{
			Scope:    "system",
			Check:    "config",
			Severity: Error,
			Message:  "workspace.toml not loaded",
		}
	}
	issues := collectConfigIssues(ws)
	if len(issues) == 0 {
		return Finding{
			Scope:    "system",
			Check:    "config",
			Severity: OK,
			Message:  "workspace.toml valid",
		}
	}
	return Finding{
		Scope:    "system",
		Check:    "config",
		Severity: Error,
		Message:  fmt.Sprintf("workspace.toml has %d issue(s): %s", len(issues), strings.Join(issues, "; ")),
		FixHint:  "edit workspace.toml by hand or re-add affected projects",
	}
}

func collectConfigIssues(ws *config.Workspace) []string {
	var issues []string
	for _, name := range sortedProjectNames(ws.Projects) {
		issues = append(issues, validateProject(name, ws.Projects[name])...)
	}
	issues = append(issues, validateDaemonDurations(ws.Daemon)...)
	return issues
}

func sortedProjectNames(projects map[string]config.Project) []string {
	out := make([]string, 0, len(projects))
	for n := range projects {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func validateProject(name string, p config.Project) []string {
	var issues []string
	if strings.TrimSpace(p.Remote) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing remote", name))
	}
	if strings.TrimSpace(p.Path) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing path", name))
	}
	if msg := validateProjectStatus(name, p.Status); msg != "" {
		issues = append(issues, msg)
	}
	if msg := validateProjectCategory(name, p.Category); msg != "" {
		issues = append(issues, msg)
	}
	return issues
}

func validateProjectStatus(name string, s config.Status) string {
	switch s {
	case config.StatusActive, config.StatusArchived, config.StatusDormant:
		return ""
	case "":
		return fmt.Sprintf("%s: missing status", name)
	}
	return fmt.Sprintf("%s: unknown status %q", name, s)
}

func validateProjectCategory(name string, c config.Category) string {
	switch c {
	case config.CategoryPersonal, config.CategoryWork, "":

		return ""
	}
	return fmt.Sprintf("%s: unknown category %q", name, c)
}

func validateDaemonDurations(d config.Daemon) []string {
	var issues []string
	for _, pair := range []struct{ name, val string }{
		{"daemon.poll_interval", d.PollInterval},
		{"daemon.stale_threshold", d.StaleThreshold},
	} {
		if pair.val == "" {
			continue
		}
		if !validDuration(pair.val) {
			issues = append(issues, fmt.Sprintf("%s %q is not a valid duration", pair.name, pair.val))
		}
	}
	return issues
}

func validDuration(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasSuffix(s, "d") {
		trimmed := strings.TrimSuffix(s, "d")
		if trimmed == "" {
			return false
		}
		var days int
		n, err := fmt.Sscanf(trimmed, "%d", &days)
		return err == nil && n == 1 && days >= 0
	}
	_, err := time.ParseDuration(s)
	return err == nil
}
