package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

func (r *Runner) systemChecks() []Finding {
	return []Finding{
		checkStaleSidecars(r.WsRoot),
		checkConflicts(r.WsRoot),
		checkConfig(r.WsRoot, r.WS, r.ConfigLoadErr),
	}
}

func checkStaleSidecars(wsRoot string) Finding {
	stale := findStaleSidecars(wsRoot)
	if len(stale) == 0 {
		return Finding{Scope: "system", Check: "sidecar", Severity: OK, Message: "no stale sidecars"}
	}
	return Finding{
		Scope: "system", Check: "sidecar", Severity: Warn,
		Message: fmt.Sprintf("stale sidecar(s): %v", sidecarKindNames(stale)),
		FixHint: "remove stale sidecar file(s)",
		Fix:     func() error { return deleteSidecars(wsRoot, stale) },
	}
}

func findStaleSidecars(wsRoot string) []sidecar.Kind {
	kinds := []sidecar.Kind{sidecar.KindBootstrap, sidecar.KindMigrate}
	var stale []sidecar.Kind
	for _, kind := range kinds {
		sc, err := sidecar.Load(wsRoot, kind)
		if err == nil && sc != nil && !sidecar.IsAlive(sc) {
			stale = append(stale, kind)
		}
	}
	return stale
}

func sidecarKindNames(kinds []sidecar.Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, string(kind))
	}
	return out
}

func deleteSidecars(wsRoot string, kinds []sidecar.Kind) error {
	for _, kind := range kinds {
		if err := sidecar.Delete(wsRoot, kind); err != nil {
			return fmt.Errorf("delete %s sidecar: %w", kind, err)
		}
	}
	return nil
}

func checkConflicts(wsRoot string) Finding {
	mine, err := loadProjectConflicts(wsRoot)
	if err != nil {
		return Finding{Scope: "system", Check: "conflicts", Severity: Warn, Message: err.Error()}
	}
	if len(mine) == 0 {
		return Finding{Scope: "system", Check: "conflicts", Severity: OK, Message: "no active conflicts"}
	}
	oldest := oldestConflict(mine)
	message := fmt.Sprintf("%d active conflict(s); oldest: %s (%s, %s ago)",
		len(mine), oldest.Kind, projectOrGlobal(oldest), humanizeAge(time.Since(oldest.DetectedAt)))
	return Finding{
		Scope: "system", Check: "conflicts", Severity: Error,
		Message: message, FixHint: "run `ws sync resolve`",
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

func checkConfig(wsRoot string, ws *config.Workspace, loadErr error) Finding {
	if loadErr != nil {
		return Finding{
			Scope: "system", Check: "config", Severity: Error,
			Message: loadErr.Error(), FixHint: "repair duplicated branch keys in workspace.toml",
			Fix: func() error { return repairWorkspaceTOML(wsRoot) },
		}
	}
	if ws == nil {
		return Finding{Scope: "system", Check: "config", Severity: Error, Message: "workspace.toml not loaded"}
	}
	issues := collectConfigIssues(ws)
	if len(issues) == 0 {
		return Finding{Scope: "system", Check: "config", Severity: OK, Message: "workspace.toml valid"}
	}
	return Finding{
		Scope: "system", Check: "config", Severity: Error,
		Message: fmt.Sprintf("workspace.toml has %d issue(s): %s", len(issues), strings.Join(issues, "; ")),
		FixHint: "edit workspace.toml by hand or re-add affected projects",
	}
}

func collectConfigIssues(ws *config.Workspace) []string {
	var issues []string
	for _, name := range sortedProjectNames(ws.Projects) {
		issues = append(issues, validateProject(name, ws.Projects[name])...)
	}
	return issues
}

func sortedProjectNames(projects map[string]config.Project) []string {
	out := make([]string, 0, len(projects))
	for name := range projects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func validateProject(name string, project config.Project) []string {
	var issues []string
	if strings.TrimSpace(project.Remote) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing remote", name))
	}
	if strings.TrimSpace(project.Path) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing path", name))
	}
	if message := validateProjectStatus(name, project.Status); message != "" {
		issues = append(issues, message)
	}
	if message := validateProjectCategory(name, project.Category); message != "" {
		issues = append(issues, message)
	}
	return issues
}

func validateProjectStatus(name string, status config.Status) string {
	switch status {
	case config.StatusActive, config.StatusArchived, config.StatusDormant:
		return ""
	case "":
		return fmt.Sprintf("%s: missing status", name)
	}
	return fmt.Sprintf("%s: unknown status %q", name, status)
}

func validateProjectCategory(name string, category config.Category) string {
	switch category {
	case config.CategoryPersonal, config.CategoryWork, "":
		return ""
	}
	return fmt.Sprintf("%s: unknown category %q", name, category)
}
