package doctor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

// systemChecks runs the four system-level checks once per invocation.
// Order is display order — daemon first because it's the broadest context,
// then sidecar → conflicts → config drills from "environment" to
// "configuration".
func (r *Runner) systemChecks() []Finding {
	return []Finding{
		checkDaemon(),
		checkStaleSidecars(r.WsRoot),
		checkConflicts(r.WsRoot),
		checkConfig(r.WS),
	}
}

// checkDaemon reports whether the background daemon is alive. We do not
// offer to start it automatically: starting a daemon is an explicit user
// action (ws daemon start), and the user's reasons for keeping it off
// (laptop battery, intentional manual sync) are outside doctor's scope.
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

// checkStaleSidecars returns one Finding per known sidecar kind that has
// a dead pid recorded on disk. A stale sidecar would normally block the
// reconciler for this workspace until removed, so this is important to
// surface even though the recovery path is trivial.
//
// If every kind is either absent or live, returns a single OK finding so
// the user sees that the check actually ran.
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

// findStaleSidecars returns the sidecar kinds whose pid is no longer
// alive in `wsRoot`. Skips kinds with no sidecar present and live ones.
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

// sidecarKindNames maps a sidecar.Kind slice to a string slice for
// the message-formatting path.
func sidecarKindNames(kinds []sidecar.Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// deleteSidecars removes every sidecar in `kinds` from `wsRoot`.
// Used as the Fix function for the stale-sidecar Finding; collapses
// the multi-kind cleanup into one user action.
func deleteSidecars(wsRoot string, kinds []sidecar.Kind) error {
	for _, k := range kinds {
		if err := sidecar.Delete(wsRoot, k); err != nil {
			return fmt.Errorf("delete %s sidecar: %w", k, err)
		}
	}
	return nil
}

// checkConflicts surfaces any entries in ~/.local/state/ws/conflicts.json
// that belong to this workspace. Doctor never auto-resolves — the
// FixHint points at `ws sync resolve`, which is the single entry point
// for conflict resolution.
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

// loadProjectConflicts opens the conflict store and returns the
// conflicts whose Workspace path resolves to wsRoot. Errors from the
// store are wrapped with a user-readable prefix so the caller can
// drop them straight into Finding.Message.
func loadProjectConflicts(wsRoot string) ([]conflict.Conflict, error) {
	store, err := conflict.Open()
	if err != nil {
		return nil, fmt.Errorf("cannot read conflict store: %w", err)
	}
	all, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("cannot list conflicts: %w", err)
	}
	absWsRoot, _ := filepath.Abs(wsRoot)
	var mine []conflict.Conflict
	for _, c := range all {
		abs, _ := filepath.Abs(c.Workspace)
		if abs == absWsRoot {
			mine = append(mine, c)
		}
	}
	return mine, nil
}

// oldestConflict picks the lowest-DetectedAt entry from a non-empty
// slice. Used by checkConflicts to surface the most-aged conflict in
// the doctor message; the full list lives in `ws sync resolve`.
func oldestConflict(conflicts []conflict.Conflict) conflict.Conflict {
	oldest := conflicts[0]
	for _, c := range conflicts[1:] {
		if c.DetectedAt.Before(oldest.DetectedAt) {
			oldest = c
		}
	}
	return oldest
}

func projectOrGlobal(c conflict.Conflict) string {
	if c.Project != "" {
		return c.Project
	}
	return "workspace"
}

// humanizeAge renders a duration in the same style as status.go's
// humanizeTime but focused on "how long has this been broken" framing.
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

// checkConfig validates the currently loaded workspace.toml: every active
// project must have a non-empty Remote and Path, its Status / Category
// must be a known enum value, and the daemon duration strings (if set)
// must parse. The goal is to catch hand-edited typos; the TOML parser
// already rejects structural errors.
//
// Duration validation mirrors status.go's parseDuration — "30d" suffix
// plus anything time.ParseDuration accepts — rather than re-deriving the
// grammar, which would drift.
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

// collectConfigIssues runs every per-field validator in the workspace
// and concatenates their issue messages, sorted-by-project for
// deterministic output. Drives both the OK / Error split in
// checkConfig and unit tests that assert specific issue strings.
func collectConfigIssues(ws *config.Workspace) []string {
	var issues []string
	for _, name := range sortedProjectNames(ws.Projects) {
		issues = append(issues, validateProject(name, ws.Projects[name])...)
	}
	issues = append(issues, validateDaemonDurations(ws.Daemon)...)
	return issues
}

// sortedProjectNames returns the project names of `projects` in
// lexical order. Used for stable check ordering in the report.
func sortedProjectNames(projects map[string]config.Project) []string {
	out := make([]string, 0, len(projects))
	for n := range projects {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// validateProject returns one issue string per per-field problem in
// the given project. Empty slice when the project record is well-formed.
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
		// "" is tolerated — category is optional.
		return ""
	}
	return fmt.Sprintf("%s: unknown category %q", name, c)
}

// validateDaemonDurations checks that any non-empty daemon duration
// strings parse as accepted Go durations (with the optional "Nd"
// extension). Returns one issue per malformed entry.
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

// validDuration mirrors status.go's parseDuration — accepts a trailing
// "d" suffix for day-granularity values (e.g. "30d") plus anything the
// stdlib time.ParseDuration accepts ("5m", "1h30m").
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
