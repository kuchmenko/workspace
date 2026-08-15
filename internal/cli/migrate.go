package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newMigrateCmd() *cobra.Command {
	var (
		all   bool
		check bool
		wip   bool
		noTUI bool
	)

	cmd := &cobra.Command{
		Use:         "migrate [project]",
		Short:       "Convert plain checkouts into the bare+worktree layout",
		Annotations: agentAnnotations("project-migrate", AgentInteractionConditional, AgentApprovalConditional, AgentEffectConditional, AgentEffectNone, "text", "0,1"),
		Long: `Convert one or all active projects from a plain 'git clone' checkout
into the worktree layout (bare repo as a sibling, main worktree in place).

By default, ws migrate launches an interactive TUI that scans every project,
shows a plan, and lets you decide per-project how to handle dirty trees,
stash entries, and detached HEAD. Pass any explicit flag (--all, --check,
--wip) or run without a TTY to switch to non-interactive mode.

Examples:
  ws migrate                 interactive TUI on every active project
  ws migrate myapp           interactive TUI on one project
  ws migrate --all           non-interactive: migrate every active project
  ws migrate --check         report which projects need migration (always non-interactive)
  ws migrate myapp --wip     non-interactive: auto-snapshot dirty changes to a WIP branch

In TUI mode, dirty/stash/detached states are resolved interactively:
  - dirty   → snapshot to wt/<machine>/migration-wip-<ts> (or skip)
  - stash   → convert each entry to a wt/<machine>/migration-stash-<ts>-N branch (or skip)
  - detached → preserve orphaned commits, then checkout default_branch (or skip)

Migration progress is stored in a sidecar at ~/.local/state/ws/migrate/.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runMigrateCheck(args)
			}

			interactive := !noTUI && !all && !wip && term.IsTerminal(int(os.Stdout.Fd()))

			if interactive {
				return runMigrateTUI(args)
			}

			if !all && len(args) != 1 {
				return errors.New("specify a project name or use --all")
			}
			if all && len(args) > 0 {
				return errors.New("cannot combine --all with a project name")
			}

			machine, err := ensureMachineName()
			if err != nil {
				return err
			}

			var targets []string
			if all {
				for name, p := range ws.Projects {
					if p.Status == config.StatusActive {
						targets = append(targets, name)
					}
				}
				sort.Strings(targets)
			} else {
				targets = args
			}

			opts := repo.MigrateOptions{
				WIP:                 wip,
				Machine:             machine,
				PromptDefaultBranch: promptDefaultBranchStdin,
				Logf: func(format string, a ...interface{}) {
					fmt.Printf("  "+format+"\n", a...)
				},
			}

			anyMigrated := false
			anyFailed := false
			migratedCount := 0
			skippedMissing := 0
			skippedAlready := 0
			for _, name := range targets {
				proj, ok := ws.Projects[name]
				if !ok {
					fmt.Printf("  skip   %s: not in workspace registry\n", name)
					continue
				}
				if proj.Status != config.StatusActive {
					fmt.Printf("  skip   %s: status=%s\n", name, proj.Status)
					continue
				}

				if all {
					switch repo.Check(wsRoot, name, proj).State {
					case "missing":
						fmt.Printf("  skip   %s: not cloned on this machine\n", name)
						skippedMissing++
						continue
					case "migrated":
						fmt.Printf("  skip   %s: already migrated\n", name)
						skippedAlready++
						continue
					case "not-a-repo":
						fmt.Printf("  skip   %s: path exists but is not a git repo\n", name)
						continue
					}
				}
				res, err := repo.MigrateProject(wsRoot, name, &proj, opts)
				if err != nil {
					if errors.Is(err, repo.ErrAlreadyMigrated) {
						fmt.Printf("  skip   %s: already migrated\n", name)
						skippedAlready++
						continue
					}
					fmt.Printf("  error  %s: %v\n", name, err)
					anyFailed = true
					continue
				}
				ws.Projects[name] = proj
				anyMigrated = true
				migratedCount++
				fmt.Printf("  done   %s → %s (%d branches preserved", name, res.BarePath, res.BranchesPushed)
				if len(res.HooksMigrated) > 0 {
					fmt.Printf(", %d hooks", len(res.HooksMigrated))
				}
				if res.WIPWorktree != "" {
					fmt.Printf(", WIP at %s", res.WIPWorktree)
				}
				fmt.Println(")")
			}

			if anyMigrated {
				if err := saveWorkspace(); err != nil {
					return err
				}
			}
			if all {
				if migratedCount == 0 && !anyFailed {
					if skippedMissing > 0 || skippedAlready > 0 {
						fmt.Printf("Nothing to migrate (%d already migrated, %d not cloned on this machine).\n", skippedAlready, skippedMissing)
					} else {
						fmt.Println("No active projects to migrate.")
					}
				} else {
					fmt.Printf("Migrated %d project(s); skipped %d already migrated, %d not cloned locally.\n", migratedCount, skippedAlready, skippedMissing)
				}
			}
			if anyFailed {
				return errors.New("some migrations failed")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "migrate every active project (non-interactive)")
	cmd.Flags().BoolVar(&check, "check", false, "report state without making changes (always non-interactive)")
	cmd.Flags().BoolVar(&wip, "wip", false, "snapshot dirty trees to a WIP branch instead of aborting (non-interactive)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "force non-interactive flow even when stdout is a TTY")
	return cmd
}

func runMigrateCheck(args []string) error {
	names := args
	if len(names) == 0 {
		for n := range ws.Projects {
			names = append(names, n)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		fmt.Println("No projects registered in the workspace registry.")
		return nil
	}
	for _, name := range names {
		proj, ok := ws.Projects[name]
		if !ok {
			fmt.Printf("  ?      %s: not in workspace registry\n", name)
			continue
		}
		r := repo.Check(wsRoot, name, proj)
		var note []string
		if r.HasStash {
			note = append(note, "stash present")
		}
		if r.IsDirty {
			note = append(note, "dirty")
		}
		if r.Detached {
			note = append(note, "detached HEAD")
		}
		if r.HooksFound > 0 {
			note = append(note, fmt.Sprintf("%d hooks", r.HooksFound))
		}
		if r.Branch != "" {
			note = append(note, "branch="+r.Branch)
		}
		extra := ""
		if len(note) > 0 {
			extra = " [" + strings.Join(note, ", ") + "]"
		}
		fmt.Printf("  %-15s %s%s\n", r.State, name, extra)
	}
	return nil
}

func ensureMachineName() (string, error) {
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return "", err
	}
	if mc.MachineName != "" {
		return mc.MachineName, nil
	}
	def := config.DefaultMachineName()
	fmt.Printf("First-time setup: pick a short machine name for branch namespacing.\n")
	fmt.Printf("This will appear in branch names like wt/<machine>/<topic>.\n")
	fmt.Printf("Suggested: %s\n", def)
	fmt.Printf("Machine name [%s]: ", def)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	clean := config.SanitizeMachineName(line)
	if clean == "" {
		return "", errors.New("machine name cannot be empty after sanitization")
	}
	mc.MachineName = clean
	if err := config.SaveMachineConfig(mc); err != nil {
		return "", err
	}
	fmt.Printf("Saved machine name: %s\n", clean)
	return clean, nil
}

func promptDefaultBranchStdin(project string, candidates []string) (string, error) {
	fmt.Printf("Default branch for %s could not be auto-detected.\n", project)
	if len(candidates) > 0 {
		fmt.Printf("Candidates found locally: %s\n", strings.Join(candidates, ", "))
	}
	def := ""
	if len(candidates) == 1 {
		def = candidates[0]
	} else if len(candidates) > 0 {
		def = candidates[0]
	}
	if def != "" {
		fmt.Printf("Default branch [%s]: ", def)
	} else {
		fmt.Printf("Default branch: ")
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		line = def
	}
	if line == "" {
		return "", errors.New("no branch entered")
	}
	return line, nil
}

type migrateStep int

const (
	mStepPlan migrateStep = iota
	mStepDecision
	mStepMigrating
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
	queue   []migratePlanItem
	cursor  int
	current migratePlanItem

	decisions map[string]migrateDecision

	successes []string
	errors    []migrateError
	skipped   int
	canceled  bool

	spinner tui.Spinner
	sidecar *repo.MigrateSidecar
}

type migrateDecision struct {
	WIP             bool
	StashBranch     bool
	CheckoutDefault bool
	Skip            bool
}

type migrateDoneMsg struct {
	index   int
	project string
	res     *repo.MigrateResult
	err     error
}

type migrateAllDoneMsg struct{}

func newMigrateModel(plan *migratePlan, machine string, resume map[string]repo.MigrateDoneEntry) migrateModel {
	sp := tui.NewSpinner()
	sp.SetStyle(tui.DotSpinner)
	sp.SetTextStyle(tui.NewStyle().Foreground("6"))

	sc := repo.NewMigrateSidecar(wsRoot)
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

func (m migrateModel) Init() tui.Cmd {
	return m.spinner.Tick
}

func (m migrateModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.KeyMsg:
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tui.Quit
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
		if _, ok := msg.(tui.KeyMsg); ok {
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m migrateModel) updatePlan(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":

			for _, s := range []migrateState{mstReady, mstDirty, mstStash, mstDetached} {
				m.queue = append(m.queue, m.plan.Bucket(s)...)
			}
			if len(m.queue) == 0 {
				m.step = mStepDone
				return m, tui.Quit
			}

			if err := repo.SaveMigrateSidecar(m.sidecar); err != nil {
				m.errors = append(m.errors, migrateError{project: "<sidecar>", err: err})
				return m, tui.Quit
			}
			notify("ws: migrate started",
				fmt.Sprintf("%s: %d projects", wsRoot, len(m.queue)))
			return m.advance()
		case "n", "N", "escape":
			m.canceled = true
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m migrateModel) advance() (tui.Model, tui.Cmd) {
	if m.cursor >= len(m.queue) {
		m.step = mStepDone
		return m, tui.Quit
	}
	m.current = m.queue[m.cursor]
	switch m.current.State {
	case mstReady:

		m.step = mStepMigrating
		m.stepChangedAt = time.Now()
		return m, tui.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
	case mstDirty, mstStash, mstDetached:
		m.step = mStepDecision
		m.stepChangedAt = time.Now()
		return m, nil
	}

	m.skipped++
	m.cursor++
	return m.advance()
}

func (m migrateModel) updateDecision(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
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
			m.canceled = true
			return m, tui.Quit
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
			m.canceled = true
			return m, tui.Quit
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
			m.canceled = true
			return m, tui.Quit
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
	return m, tui.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
}

func (m migrateModel) startMigrate(index int) tui.Cmd {
	item := m.queue[index]
	dec := m.decisions[item.Name]
	machine := m.machine
	return func() tui.Msg {
		proj := item.Project
		opts := repo.MigrateOptions{
			WIP:             dec.WIP,
			StashBranch:     dec.StashBranch,
			CheckoutDefault: dec.CheckoutDefault,
			Machine:         machine,
		}
		res, err := repo.MigrateProject(wsRoot, item.Name, &proj, opts)
		return migrateDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

func (m migrateModel) updateMigrating(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case migrateDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, migrateError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)
			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = repo.SaveMigrateSidecar(m.sidecar)
			}
		}
		m.cursor++
		return m.advance()
	case migrateAllDoneMsg:
		m.step = mStepDone
		return m, tui.Quit
	}
	return m, nil
}

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

	existing, err := repo.LoadMigrateSidecar(wsRoot)
	if err != nil {
		return fmt.Errorf("read migrate sidecar: %w", err)
	}
	resumeFrom := map[string]repo.MigrateDoneEntry{}
	if existing != nil {
		if repo.MigrateSidecarIsAlive(existing) {
			return fmt.Errorf("migrate already running (pid %d, started %s)",
				existing.Meta.PID, existing.Meta.Started.Local().Format(time.RFC3339))
		}

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
			if err := repo.DeleteMigrateSidecar(wsRoot); err != nil {
				return err
			}
		default:
			fmt.Println("Aborted.")
			return nil
		}
	}

	model := newMigrateModel(plan, machine, resumeFrom)
	p := tui.NewProgram(model, tui.WithAltScreen())
	finalRaw, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("TUI crashed: %w", runErr)
	}
	final := finalRaw.(migrateModel)

	if final.canceled {
		fmt.Println("Migrate canceled by user.")
		return nil
	}

	if len(final.errors) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, errorBannerStyle.Render("Migrate finished with errors:"))
		for _, e := range final.errors {
			fmt.Fprintf(os.Stderr, "\n  %s\n", e.project)
			fmt.Fprintln(os.Stderr, indent(strings.TrimSpace(e.err.Error()), "    "))
		}
	}

	if final.sidecar != nil && len(final.sidecar.Done) > 0 {
		if err := commitMigrate(final.sidecar); err != nil {
			return fmt.Errorf("commit migrate: %w", err)
		}
		if err := repo.DeleteMigrateSidecar(wsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove sidecar: %v\n", err)
		}
	}

	migrated := len(final.successes)
	failed := len(final.errors)
	skipped := final.skipped
	total := migrated + failed + skipped
	fmt.Printf("\nMigrate complete: %d migrated, %d failed, %d skipped (of %d ready).\n", migrated, failed, skipped, total)
	if failed > 0 {
		notify("ws: migrate finished with errors",
			fmt.Sprintf("%d/%d migrated — see terminal", migrated, total))
		return errors.New("migrate finished with errors")
	}
	if migrated > 0 {
		notify("ws: migrate finished",
			fmt.Sprintf("%d projects migrated", migrated))
	}
	return nil
}

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
		check := repo.Check(wsRoot, name, proj)
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
		default:
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

func commitMigrate(sc *repo.MigrateSidecar) error {
	if err := loadCurrentWorkspace(); err != nil {
		return err
	}
	freshWS := ws
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
	mstAlready
	mstMissing
	mstNotRepo
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
	Check   repo.CheckResult
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
