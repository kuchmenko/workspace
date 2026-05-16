package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/migrate"
)

// runMigrateTUI is the entry point used by `ws migrate` (no flags) and
// `ws migrate <name>`. It scans the workspace, builds a plan, and runs the
// per-project flow inside a bubbletea program.
//
// args is either empty (all active projects) or a single project name. The
// CLI dispatcher already validated the count.
func runMigrateTUI(args []string) error {
	machine, err := ensureMachineName()
	if err != nil {
		return err
	}

	plan := buildMigratePlan(args)
	if len(plan.Items) == 0 {
		fmt.Println("No active projects to migrate.")
		return nil
	}

	// Sidecar pre-check: another migrate running? Stale crash to resume?
	existing, err := migrate.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("read migrate sidecar: %w", err)
	}
	resumeFrom := map[string]migrate.DoneEntry{}
	if existing != nil {
		if migrate.IsAlive(existing) {
			return fmt.Errorf("migrate already running (pid %d, started %s)",
				existing.Meta.PID, existing.Meta.Started.Local().Format(time.RFC3339))
		}
		// Stale: ask the user what to do.
		fmt.Printf("Found incomplete migrate from %s (pid %d, %d projects done).\n",
			existing.Meta.Started.Local().Format(time.RFC3339),
			existing.Meta.PID, len(existing.Done))
		fmt.Print("Resume? [Y/n/discard]: ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "", "y", "yes":
			resumeFrom, err = existing.DoneEntries()
			if err != nil {
				return fmt.Errorf("read sidecar entries: %w", err)
			}
		case "d", "discard":
			if err := migrate.Delete(wsRoot); err != nil {
				return err
			}
		default:
			fmt.Println("Aborted.")
			return nil
		}
	}

	model := newMigrateModel(plan, machine, resumeFrom)
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalRaw, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("TUI crashed: %w", runErr)
	}
	final := finalRaw.(migrateModel)

	if final.canceled {
		fmt.Println("Migrate canceled by user.")
		return nil
	}

	// Post-TUI: print full per-project errors. Long git stderr would break
	// the TUI box, so we surface it here.
	if len(final.errors) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, errorBannerStyle.Render("Migrate finished with errors:"))
		for _, e := range final.errors {
			fmt.Fprintf(os.Stderr, "\n  %s\n", e.project)
			fmt.Fprintln(os.Stderr, indent(strings.TrimSpace(e.err.Error()), "    "))
		}
	}

	// Final commit step: persist default_branch values from the sidecar.
	if final.sidecar != nil && len(final.sidecar.Done) > 0 {
		if err := commitMigrate(final.sidecar); err != nil {
			return fmt.Errorf("commit migrate: %w", err)
		}
		if err := migrate.Delete(wsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove sidecar: %v\n", err)
		}
	}

	migrated := len(final.successes)
	failed := len(final.errors)
	skipped := final.skipped
	total := migrated + failed + skipped
	fmt.Printf("\nMigrate complete: %d migrated, %d failed, %d skipped (of %d ready).\n", migrated, failed, skipped, total)
	if failed > 0 {
		conflict.Notify("ws: migrate finished with errors",
			fmt.Sprintf("%d/%d migrated — see terminal", migrated, total))
		return errors.New("migrate finished with errors")
	}
	if migrated > 0 {
		conflict.Notify("ws: migrate finished",
			fmt.Sprintf("%d projects migrated", migrated))
	}
	return nil
}

// buildMigratePlan walks ws.Projects, classifies each into a migrateState,
// and returns the ordered plan. Filtering by `only` (when non-empty)
// restricts the scan to one project name — used by `ws migrate <name>`.
func buildMigratePlan(only []string) *migratePlan {
	wantOnly := map[string]bool{}
	for _, n := range only {
		wantOnly[n] = true
	}
	plan := &migratePlan{}
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		if len(wantOnly) > 0 && !wantOnly[name] {
			continue
		}
		check := migrate.Check(wsRoot, name, proj)
		item := migratePlanItem{
			Name:    name,
			Project: proj,
			Check:   check,
		}
		switch check.State {
		case "migrated":
			item.State = mstAlready
		case "missing":
			item.State = mstMissing
		case "not-a-repo":
			item.State = mstNotRepo
		default: // "needs-migration"
			switch {
			case check.HasStash:
				item.State = mstStash
			case check.Detached:
				item.State = mstDetached
			case check.IsDirty:
				item.State = mstDirty
			default:
				item.State = mstReady
			}
		}
		plan.Items = append(plan.Items, item)
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].Name < plan.Items[j].Name })
	return plan
}

// commitMigrate re-reads workspace.toml from disk and applies default_branch
// values captured in the sidecar in one atomic write. Symmetric with
// commitBootstrap.
func commitMigrate(sc *migrate.Sidecar) error {
	freshWS, err := config.Load(wsRoot)
	if err != nil {
		return err
	}
	entries, err := sc.DoneEntries()
	if err != nil {
		return err
	}
	for name, entry := range entries {
		proj, ok := freshWS.Projects[name]
		if !ok {
			continue
		}
		if proj.DefaultBranch == "" && entry.DefaultBranch != "" {
			proj.DefaultBranch = entry.DefaultBranch
			freshWS.Projects[name] = proj
		}
	}
	ws = freshWS
	return saveWorkspace()
}

type migrateState int

const (
	mstReady migrateState = iota
	mstDirty
	mstStash
	mstDetached
	mstAlready // already migrated, skip
	mstMissing // not on disk, skip
	mstNotRepo // garbage, skip
)

func (s migrateState) label() string {
	switch s {
	case mstReady:
		return "ready"
	case mstDirty:
		return "dirty"
	case mstStash:
		return "stash"
	case mstDetached:
		return "detached HEAD"
	case mstAlready:
		return "already migrated"
	case mstMissing:
		return "not on disk"
	case mstNotRepo:
		return "not a git repo"
	}
	return ""
}

type migratePlanItem struct {
	Name    string
	Project config.Project
	Check   migrate.CheckResult
	State   migrateState
}

type migratePlan struct {
	Items []migratePlanItem
}

func (p *migratePlan) Bucket(s migrateState) []migratePlanItem {
	var out []migratePlanItem
	for _, it := range p.Items {
		if it.State == s {
			out = append(out, it)
		}
	}
	return out
}
