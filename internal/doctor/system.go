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
