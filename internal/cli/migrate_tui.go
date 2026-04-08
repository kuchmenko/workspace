package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	if final.cancelled {
		fmt.Println("Migrate cancelled by user.")
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

// =============================================================================
// Plan model
// =============================================================================

type migrateState int

const (
	mstReady migrateState = iota
	mstDirty
	mstStash
	mstDetached
	mstAlready  // already migrated, skip
	mstMissing  // not on disk, skip
	mstNotRepo  // garbage, skip
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

// =============================================================================
// Bubbletea model
// =============================================================================

type migrateStep int

const (
	mStepPlan migrateStep = iota
	mStepDecision    // per-project decision (dirty/stash/detached)
	mStepMigrating   // running migrate.MigrateProject
	mStepDone
)

type migrateError struct {
	project string
	err     error
}

type migrateModel struct {
	step          migrateStep
	stepChangedAt time.Time

	machine string
	plan    *migratePlan
	queue   []migratePlanItem // projects pending action, in order
	cursor  int               // index into queue
	current migratePlanItem   // active project

	// Decisions accumulated per project before the migration runs.
	decisions map[string]migrateDecision

	successes []string
	errors    []migrateError
	skipped   int
	cancelled bool

	spinner spinner.Model
	sidecar *migrate.Sidecar
}

// migrateDecision captures the user's per-project answer to a state-specific
// prompt. Empty fields default to "abort" semantics.
type migrateDecision struct {
	WIP             bool
	StashBranch     bool
	CheckoutDefault bool
	Skip            bool
}

type migrateDoneMsg struct {
	index   int
	project string
	res     *migrate.Result
	err     error
}

type migrateAllDoneMsg struct{}

func newMigrateModel(plan *migratePlan, machine string, resume map[string]migrate.DoneEntry) migrateModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	sc := migrate.New(wsRoot)
	for k, v := range resume {
		_ = sc.Set(k, v)
	}

	return migrateModel{
		step:      mStepPlan,
		machine:   machine,
		plan:      plan,
		decisions: make(map[string]migrateDecision),
		spinner:   sp,
		sidecar:   sc,
	}
}

func (m migrateModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m migrateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
	}

	switch m.step {
	case mStepPlan:
		return m.updatePlan(msg)
	case mStepDecision:
		return m.updateDecision(msg)
	case mStepMigrating:
		return m.updateMigrating(msg)
	case mStepDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m migrateModel) updatePlan(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			// Build queue: ready + dirty + stash + detached, in that order.
			// already/missing/not-a-repo are skipped silently.
			for _, s := range []migrateState{mstReady, mstDirty, mstStash, mstDetached} {
				m.queue = append(m.queue, m.plan.Bucket(s)...)
			}
			if len(m.queue) == 0 {
				m.step = mStepDone
				return m, tea.Quit
			}
			// Persist sidecar with our pid before any migrate runs.
			if err := migrate.Save(m.sidecar); err != nil {
				m.errors = append(m.errors, migrateError{project: "<sidecar>", err: err})
				return m, tea.Quit
			}
			conflict.Notify("ws: migrate started",
				fmt.Sprintf("%s: %d projects", wsRoot, len(m.queue)))
			return m.advance()
		case "n", "N", "escape":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// advance moves from one queue item to the next. If the next item needs a
// per-project decision, switch to mStepDecision; otherwise kick off
// migration directly.
func (m migrateModel) advance() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.queue) {
		m.step = mStepDone
		return m, tea.Quit
	}
	m.current = m.queue[m.cursor]
	switch m.current.State {
	case mstReady:
		// No decision needed. Migrate immediately.
		m.step = mStepMigrating
		m.stepChangedAt = time.Now()
		return m, tea.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
	case mstDirty, mstStash, mstDetached:
		m.step = mStepDecision
		m.stepChangedAt = time.Now()
		return m, nil
	}
	// Unknown — skip.
	m.skipped++
	m.cursor++
	return m.advance()
}

func (m migrateModel) updateDecision(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	dec := migrateDecision{}
	resolved := false
	switch m.current.State {
	case mstDirty:
		switch key.String() {
		case "w", "W":
			dec.WIP = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.cancelled = true
			return m, tea.Quit
		}
	case mstStash:
		switch key.String() {
		case "b", "B":
			dec.StashBranch = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.cancelled = true
			return m, tea.Quit
		}
	case mstDetached:
		switch key.String() {
		case "c", "C":
			dec.CheckoutDefault = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	if !resolved {
		return m, nil
	}
	m.decisions[m.current.Name] = dec
	if dec.Skip {
		m.skipped++
		m.cursor++
		return m.advance()
	}
	m.step = mStepMigrating
	m.stepChangedAt = time.Now()
	return m, tea.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
}

// startMigrate runs MigrateProject in a goroutine and returns a tea.Cmd that
// emits migrateDoneMsg on completion.
func (m migrateModel) startMigrate(index int) tea.Cmd {
	item := m.queue[index]
	dec := m.decisions[item.Name]
	machine := m.machine
	return func() tea.Msg {
		proj := item.Project
		opts := migrate.Options{
			WIP:             dec.WIP,
			StashBranch:     dec.StashBranch,
			CheckoutDefault: dec.CheckoutDefault,
			Machine:         machine,
		}
		res, err := migrate.MigrateProject(wsRoot, item.Name, &proj, opts)
		return migrateDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

func (m migrateModel) updateMigrating(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case migrateDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, migrateError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)
			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = migrate.Save(m.sidecar)
			}
		}
		m.cursor++
		return m.advance()
	case migrateAllDoneMsg:
		m.step = mStepDone
		return m, tea.Quit
	}
	return m, nil
}

// =============================================================================
// Views
// =============================================================================

func (m migrateModel) View() string {
	switch m.step {
	case mStepPlan:
		return m.viewPlan()
	case mStepDecision:
		return m.viewDecision()
	case mStepMigrating:
		return m.viewMigrating()
	case mStepDone:
		return m.viewDone()
	}
	return ""
}

func (m migrateModel) viewPlan() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrate plan "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	rows := []struct {
		state migrateState
		mark  string
	}{
		{mstReady, bsArrowStyle.Render("→")},
		{mstDirty, bsWarnStyle.Render("●")},
		{mstStash, bsWarnStyle.Render("●")},
		{mstDetached, bsWarnStyle.Render("●")},
		{mstAlready, bsCheckStyle.Render("✓")},
		{mstMissing, bsDimStyle.Render("⊘")},
		{mstNotRepo, bsErrStyle.Render("✗")},
	}
	for _, row := range rows {
		items := m.plan.Bucket(row.state)
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s (%d)\n", row.mark, bsHeaderStyle.Render(row.state.label()), len(items))
		max := len(items)
		if max > 8 {
			max = 8
		}
		for i := 0; i < max; i++ {
			fmt.Fprintf(&b, "      %s\n", items[i].Name)
		}
		if len(items) > max {
			fmt.Fprintf(&b, "      %s\n", bsDimStyle.Render(fmt.Sprintf("… and %d more", len(items)-max)))
		}
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[Y] proceed   [n/esc] cancel"))
	return b.String()
}

func (m migrateModel) viewDecision() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Decision needed "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  Project: %s\n", bsHeaderStyle.Render(m.current.Name))
	fmt.Fprintf(&b, "  State:   %s\n\n", bsWarnStyle.Render(m.current.State.label()))

	switch m.current.State {
	case mstDirty:
		b.WriteString("  Working tree has uncommitted changes.\n\n")
		b.WriteString("    [w] snapshot to wt/" + m.machine + "/migration-wip-<ts> and migrate\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	case mstStash:
		b.WriteString("  Repository has stash entries (would be lost on bare clone).\n\n")
		b.WriteString("    [b] convert each stash to wt/" + m.machine + "/migration-stash-<ts>-N branch and migrate\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	case mstDetached:
		b.WriteString("  HEAD is detached. Migration needs to attach to a branch.\n\n")
		b.WriteString("    [c] checkout default_branch (orphaned commits saved to wt/" + m.machine + "/migration-detached-<ts>)\n")
		b.WriteString("    [s] skip this project\n")
		b.WriteString("    [a] abort migrate\n")
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("press the bracketed letter to choose"))
	return b.String()
}

func (m migrateModel) viewMigrating() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrating "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	total := len(m.queue)
	done := m.cursor
	bar := renderProgressBar(done, total, 30)
	fmt.Fprintf(&b, "  %s  %d / %d\n\n", bar, done, total)

	if m.cursor < total {
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), m.current.Name)
		fmt.Fprintf(&b, "    %s\n", bsDimStyle.Render(m.current.Project.Path))
	}

	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n%s %d failed (full errors after exit)\n",
			bsErrStyle.Render("✗"), len(m.errors))
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[ctrl+c] abort"))
	return b.String()
}

func (m migrateModel) viewDone() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Migrate finished "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d migrated\n", bsCheckStyle.Render("✓"), len(m.successes))
	if m.skipped > 0 {
		fmt.Fprintf(&b, "  %s %d skipped\n", bsDimStyle.Render("⊘"), m.skipped)
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "  %s %d failed\n", bsErrStyle.Render("✗"), len(m.errors))
		b.WriteString("\n")
		b.WriteString(bsDimStyle.Render("  Full errors will be printed after exit."))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[any key] exit"))
	return b.String()
}
