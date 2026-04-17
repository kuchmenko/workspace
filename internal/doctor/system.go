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
	if len(stale) == 0 {
		return Finding{
			Scope:    "system",
			Check:    "sidecar",
			Severity: OK,
			Message:  "no stale sidecars",
		}
	}
	// Collapse multi-kind stale into one finding; the fix removes all of
	// them. Listing each separately would spam the report for a one-step
	// recovery.
	kindNames := make([]string, 0, len(stale))
	for _, k := range stale {
		kindNames = append(kindNames, string(k))
	}
	return Finding{
		Scope:    "system",
		Check:    "sidecar",
		Severity: Warn,
		Message:  fmt.Sprintf("stale sidecar(s) blocking daemon: %v", kindNames),
		FixHint:  "remove stale sidecar file(s)",
		Fix: func() error {
			for _, k := range stale {
				if err := sidecar.Delete(wsRoot, k); err != nil {
					return fmt.Errorf("delete %s sidecar: %w", k, err)
				}
			}
			return nil
		},
	}
}

// checkConflicts surfaces any entries in ~/.local/state/ws/conflicts.json
// that belong to this workspace. Doctor never auto-resolves — the
// FixHint points at `ws sync resolve`, which is the single entry point
// for conflict resolution.
func checkConflicts(wsRoot string) Finding {
	store, err := conflict.Open()
	if err != nil {
		return Finding{
			Scope:    "system",
			Check:    "conflicts",
			Severity: Warn,
			Message:  fmt.Sprintf("cannot read conflict store: %v", err),
		}
	}
	all, err := store.List()
	if err != nil {
		return Finding{
			Scope:    "system",
			Check:    "conflicts",
			Severity: Warn,
			Message:  fmt.Sprintf("cannot list conflicts: %v", err),
		}
	}
	absWsRoot, _ := filepath.Abs(wsRoot)
	var mine []conflict.Conflict
	for _, c := range all {
		abs, _ := filepath.Abs(c.Workspace)
		if abs == absWsRoot {
			mine = append(mine, c)
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
	// Pick the oldest to surface in the message; the full list is left for
	// `ws sync resolve` which has proper TUI.
	oldest := mine[0]
	for _, c := range mine[1:] {
		if c.DetectedAt.Before(oldest.DetectedAt) {
			oldest = c
		}
	}
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

	var issues []string

	names := make([]string, 0, len(ws.Projects))
	for n := range ws.Projects {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		p := ws.Projects[name]
		if strings.TrimSpace(p.Remote) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing remote", name))
		}
		if strings.TrimSpace(p.Path) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing path", name))
		}
		switch p.Status {
		case config.StatusActive, config.StatusArchived, config.StatusDormant:
		case "":
			issues = append(issues, fmt.Sprintf("%s: missing status", name))
		default:
			issues = append(issues, fmt.Sprintf("%s: unknown status %q", name, p.Status))
		}
		switch p.Category {
		case config.CategoryPersonal, config.CategoryWork:
		case "":
			// Category is optional — tolerate empty.
		default:
			issues = append(issues, fmt.Sprintf("%s: unknown category %q", name, p.Category))
		}
	}

	if s := ws.Daemon.PollInterval; s != "" {
		if !validDuration(s) {
			issues = append(issues, fmt.Sprintf("daemon.poll_interval %q is not a valid duration", s))
		}
	}
	if s := ws.Daemon.StaleThreshold; s != "" {
		if !validDuration(s) {
			issues = append(issues, fmt.Sprintf("daemon.stale_threshold %q is not a valid duration", s))
		}
	}

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
