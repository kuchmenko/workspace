package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/spf13/cobra"
)

func newBootstrapCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "bootstrap [project]",
		Short: "Clone projects from workspace.toml that are missing on this machine",
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "On a fresh machine, clone all projects listed in workspace.toml directly into the bare+worktree layout",
		},
		Long: `Materialize projects listed in workspace.toml into the bare+worktree
layout. On a fresh machine where workspace.toml has been pulled but nothing
is cloned yet, 'ws bootstrap' walks the registry and clones each missing
project directly into the canonical layout.

Bootstrap is interactive: it shows a plan of what will be done, prompts for
the default branch when it cannot be auto-detected, and surfaces any
errors before continuing.

Bootstrap is crash-safe via a sidecar progress file at
~/.local/state/ws/bootstrap/. While bootstrap is running, the daemon pauses
all sync activity for that workspace to avoid races and half-pushed state.

Examples:
  ws bootstrap                clone every active project missing locally
  ws bootstrap myapp          clone one specific project
  ws bootstrap --dry-run      show plan without cloning`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(args, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show plan without cloning")
	return cmd
}

func runBootstrap(args []string, dryRun bool) error {
	plan := bootstrap.ScanPlan(wsRoot, ws, args)
	if len(plan.Items) == 0 {
		fmt.Println("No active projects to bootstrap.")
		return nil
	}

	// Sidecar pre-check: another bootstrap running? Stale crash to resume?
	existing, err := bootstrap.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("read sidecar: %w", err)
	}
	resumeFrom := map[string]bootstrap.DoneEntry{}
	if existing != nil {
		if bootstrap.IsAlive(existing) {
			return fmt.Errorf("bootstrap already running (pid %d, started %s)",
				existing.Meta.PID, existing.Meta.Started.Local().Format(time.RFC3339))
		}
		// Stale: ask the user what to do.
		fmt.Printf("Found incomplete bootstrap from %s (pid %d, %d projects done).\n",
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
			if err := bootstrap.Delete(wsRoot); err != nil {
				return err
			}
		default:
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Dry-run: render the plan summary and exit. Never touches the sidecar.
	if dryRun {
		printPlanText(plan)
		return nil
	}

	// Filter out anything we already finished in a previous (resumed) run.
	toClone := []bootstrap.PlanItem{}
	for _, it := range plan.Bucket(bootstrap.StateMissing) {
		if _, done := resumeFrom[it.Name]; done {
			continue
		}
		toClone = append(toClone, it)
	}
	if len(toClone) == 0 && len(resumeFrom) == 0 {
		printPlanText(plan)
		fmt.Println("Nothing to clone.")
		return nil
	}

	model := newBootstrapModel(plan, toClone, resumeFrom)
	p := tea.NewProgram(model, tea.WithAltScreen())
	program = p
	defer func() { program = nil }()
	finalRaw, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("TUI crashed: %w", runErr)
	}
	final := finalRaw.(bootstrapModel)

	// Errors and notifications happen AFTER the TUI exits so the terminal is
	// clean and full git stderr can be printed without breaking layout.
	if final.cancelled {
		fmt.Println("Bootstrap cancelled by user.")
		return nil
	}

	// Per spec, all clone errors are surfaced in full here.
	if len(final.errors) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, errorBannerStyle.Render("Bootstrap finished with errors:"))
		for _, e := range final.errors {
			fmt.Fprintf(os.Stderr, "\n  %s\n", e.project)
			fmt.Fprintln(os.Stderr, indent(strings.TrimSpace(e.err.Error()), "    "))
		}
	}

	// Final commit step: re-read workspace.toml and persist default_branch
	// values from the sidecar in one atomic write.
	if final.sidecar != nil && len(final.sidecar.Done) > 0 {
		if err := commitBootstrap(final.sidecar); err != nil {
			return fmt.Errorf("commit bootstrap: %w", err)
		}
		// Best-effort sidecar cleanup. Failure here is non-fatal — the next
		// run will treat it as stale.
		if err := bootstrap.Delete(wsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove sidecar: %v\n", err)
		}
	}

	// Final summary + system notification.
	cloned := len(final.successes)
	failed := len(final.errors)
	total := cloned + failed
	fmt.Printf("\nBootstrap complete: %d cloned, %d failed (of %d planned).\n", cloned, failed, total)
	if failed > 0 {
		conflict.Notify("ws: bootstrap finished with errors",
			fmt.Sprintf("%d/%d cloned — see terminal", cloned, total))
	} else if cloned > 0 {
		conflict.Notify("ws: bootstrap finished",
			fmt.Sprintf("%d projects cloned", cloned))
	}

	if failed > 0 {
		return errors.New("bootstrap finished with errors")
	}
	return nil
}

// commitBootstrap re-reads workspace.toml from disk (in case the user
// hand-edited it during a long bootstrap), applies default_branch values
// captured in the sidecar, and saves once. Only fields not already populated
// are touched, so we never overwrite the user's intent.
func commitBootstrap(sc *bootstrap.Sidecar) error {
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
	// Swap into the package-level ws so saveWorkspace() picks it up.
	ws = freshWS
	return saveWorkspace()
}

func printPlanText(plan *bootstrap.Plan) {
	fmt.Println("Bootstrap plan:")
	for _, s := range []bootstrap.State{
		bootstrap.StateMissing,
		bootstrap.StatePresent,
		bootstrap.StateNeedsMigrate,
		bootstrap.StateBlocked,
		bootstrap.StateSelf,
	} {
		items := plan.Bucket(s)
		if len(items) == 0 {
			continue
		}
		fmt.Printf("  %s (%d)\n", s, len(items))
		for _, it := range items {
			if it.Reason != "" {
				fmt.Printf("    - %-30s %s\n", it.Name, it.Reason)
			} else {
				fmt.Printf("    - %s\n", it.Name)
			}
		}
	}
}

// =============================================================================
// Bubbletea model
// =============================================================================

type bootstrapStep int

const (
	bsStepPlan bootstrapStep = iota
	bsStepCloning
	bsStepBranchPrompt
	bsStepDone
)

type bootstrapError struct {
	project string
	err     error
}

type bootstrapModel struct {
	step          bootstrapStep
	stepChangedAt time.Time
	width         int
	height        int

	plan      *bootstrap.Plan
	toClone   []bootstrap.PlanItem
	current   int // index into toClone
	successes []string
	errors    []bootstrapError
	cancelled bool

	spinner spinner.Model
	sidecar *bootstrap.Sidecar

	// Branch-prompt sub-state
	branchProject    string
	branchCandidates []string
	branchCursor     int
	branchInput      textinput.Model
	branchInputMode  bool
	branchAnswer     chan branchAnswer
}

type branchAnswer struct {
	branch string
	err    error
}

// Custom messages for the async clone loop.
type cloneStartMsg struct{ index int }
type cloneDoneMsg struct {
	index   int
	project string
	res     *clone.Result
	err     error
}
type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

func newBootstrapModel(plan *bootstrap.Plan, toClone []bootstrap.PlanItem, resume map[string]bootstrap.DoneEntry) bootstrapModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	ti := textinput.New()
	ti.Placeholder = "branch name"
	ti.CharLimit = 80

	// Initialize sidecar (in-memory only — written to disk after first
	// successful clone, so a Ctrl+C on the plan screen leaves no trace).
	sc := bootstrap.New(wsRoot)
	for k, v := range resume {
		_ = sc.Set(k, v)
	}

	return bootstrapModel{
		step:        bsStepPlan,
		plan:        plan,
		toClone:     toClone,
		spinner:     sp,
		sidecar:     sc,
		branchInput: ti,
	}
}

func (m bootstrapModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m bootstrapModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Debounce immediately after step transitions to avoid phantom inputs.
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
	}

	switch m.step {
	case bsStepPlan:
		return m.updatePlan(msg)
	case bsStepCloning:
		return m.updateCloning(msg)
	case bsStepBranchPrompt:
		return m.updateBranchPrompt(msg)
	case bsStepDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m bootstrapModel) updatePlan(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			if len(m.toClone) == 0 {
				m.step = bsStepDone
				return m, tea.Quit
			}
			// Persist sidecar with our pid before any clone runs.
			if err := bootstrap.Save(m.sidecar); err != nil {
				m.errors = append(m.errors, bootstrapError{project: "<sidecar>", err: err})
				return m, tea.Quit
			}
			conflict.Notify("ws: bootstrap started",
				fmt.Sprintf("%s: cloning %d projects", wsRoot, len(m.toClone)))
			m.step = bsStepCloning
			m.stepChangedAt = time.Now()
			return m, tea.Batch(m.spinner.Tick, m.startClone(0))
		case "n", "N", "escape":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// startClone returns a tea.Cmd that runs CloneIntoLayout for toClone[index]
// in a goroutine and emits cloneDoneMsg when finished. Branch prompts during
// the clone are routed back through needsBranchMsg → updateBranchPrompt and
// resolved via a channel.
func (m bootstrapModel) startClone(index int) tea.Cmd {
	if index >= len(m.toClone) {
		return func() tea.Msg { return allDoneMsg{} }
	}
	item := m.toClone[index]
	return func() tea.Msg {
		proj := item.Project
		// PromptDefaultBranch bridges into the TUI: send a needsBranchMsg
		// from inside the goroutine using p.Send via the global program?
		// We don't have that handle here, so use a channel-based approach:
		// the prompt callback parks on a channel, the TUI replies via the
		// same channel after the user picks a branch.
		ch := make(chan branchAnswer, 1)
		opts := clone.Options{
			Logf: func(format string, args ...interface{}) {
				// no-op; TUI shows progress, full log goes to debug if needed
			},
			PromptDefaultBranch: func(name string, candidates []string) (string, error) {
				// Send a request into the bubbletea queue and block until
				// the model writes back into ch.
				program.Send(needsBranchMsg{
					project:    name,
					candidates: candidates,
					answer:     ch,
				})
				ans := <-ch
				return ans.branch, ans.err
			},
		}
		res, err := clone.CloneIntoLayout(wsRoot, item.Name, &proj, opts)
		// proj is local to this goroutine; the resolved default_branch is
		// returned via res for the main loop to record into the sidecar.
		return cloneDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

type allDoneMsg struct{}

// program is the running tea.Program. We need a global handle to it so the
// PromptDefaultBranch callback (running in a worker goroutine) can post
// messages back into the TUI loop. Set in runBootstrap before p.Run().
var program *tea.Program

func (m bootstrapModel) updateCloning(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case needsBranchMsg:
		// Pause clone progress and switch to the branch-prompt sub-step.
		m.step = bsStepBranchPrompt
		m.stepChangedAt = time.Now()
		m.branchProject = msg.project
		m.branchCandidates = msg.candidates
		m.branchCursor = 0
		m.branchInputMode = false
		m.branchAnswer = msg.answer
		m.branchInput.SetValue("")
		return m, nil

	case cloneDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, bootstrapError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)
			// Persist progress immediately so a crash doesn't lose work.
			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = bootstrap.Save(m.sidecar)
			}
		}
		m.current = msg.index + 1
		// Periodic notify-send progress (every 5 clones).
		if m.current > 0 && m.current%5 == 0 && m.current < len(m.toClone) {
			conflict.Notify("ws: bootstrap progress",
				fmt.Sprintf("%d/%d cloned", m.current, len(m.toClone)))
		}
		if m.current >= len(m.toClone) {
			m.step = bsStepDone
			return m, tea.Quit
		}
		return m, m.startClone(m.current)

	case allDoneMsg:
		m.step = bsStepDone
		return m, tea.Quit
	}
	return m, nil
}

func (m bootstrapModel) updateBranchPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		// Edit-mode: free-text branch input
		if m.branchInputMode {
			switch key.String() {
			case "enter":
				val := strings.TrimSpace(m.branchInput.Value())
				if val == "" {
					return m, nil
				}
				m.resolveBranch(val, nil)
				return m, m.startClone(m.current)
			case "escape":
				m.branchInputMode = false
				return m, nil
			}
			var cmd tea.Cmd
			m.branchInput, cmd = m.branchInput.Update(msg)
			return m, cmd
		}

		switch key.String() {
		case "up", "k":
			if m.branchCursor > 0 {
				m.branchCursor--
			}
		case "down", "j":
			if m.branchCursor < len(m.branchCandidates)-1 {
				m.branchCursor++
			}
		case "enter":
			if len(m.branchCandidates) == 0 {
				m.branchInputMode = true
				return m, m.branchInput.Focus()
			}
			m.resolveBranch(m.branchCandidates[m.branchCursor], nil)
			m.step = bsStepCloning
			m.stepChangedAt = time.Now()
			return m, nil
		case "i":
			m.branchInputMode = true
			return m, m.branchInput.Focus()
		case "escape":
			// User refuses to pick → treat as error for this project.
			m.resolveBranch("", errors.New("user cancelled branch selection"))
			m.step = bsStepCloning
			m.stepChangedAt = time.Now()
			return m, nil
		}
	}
	return m, nil
}

// resolveBranch unblocks the worker goroutine waiting for a branch answer.
func (m *bootstrapModel) resolveBranch(branch string, err error) {
	if m.branchAnswer == nil {
		return
	}
	m.branchAnswer <- branchAnswer{branch: branch, err: err}
	m.branchAnswer = nil
}

// =============================================================================
// Views
// =============================================================================

func (m bootstrapModel) View() string {
	switch m.step {
	case bsStepPlan:
		return m.viewPlan()
	case bsStepCloning:
		return m.viewCloning()
	case bsStepBranchPrompt:
		return m.viewBranchPrompt()
	case bsStepDone:
		return m.viewDone()
	}
	return ""
}

func (m bootstrapModel) viewPlan() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Bootstrap plan "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	rows := []struct {
		state bootstrap.State
		label string
		mark  string
	}{
		{bootstrap.StateMissing, "will clone", bsArrowStyle.Render("→")},
		{bootstrap.StatePresent, "already present", bsCheckStyle.Render("✓")},
		{bootstrap.StateNeedsMigrate, "needs migration", bsWarnStyle.Render("⚠")},
		{bootstrap.StateBlocked, "path blocked", bsErrStyle.Render("✗")},
		{bootstrap.StateSelf, "self (skipped)", bsDimStyle.Render("⊘")},
	}
	for _, row := range rows {
		items := m.plan.Bucket(row.state)
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s (%d)\n", row.mark, bsHeaderStyle.Render(row.label), len(items))
		// Truncate large lists in the TUI; full list still shown in dry-run.
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
	if len(m.toClone) == 0 {
		b.WriteString(bsDimStyle.Render("Nothing to clone."))
		b.WriteString("\n")
	}
	b.WriteString(bsHelpStyle.Render("[Y] proceed   [n/esc] cancel"))
	return b.String()
}

func (m bootstrapModel) viewCloning() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Cloning "))
	b.WriteString("\n\n")
	b.WriteString(bsDimStyle.Render(wsRoot))
	b.WriteString("\n\n")

	total := len(m.toClone)
	done := m.current
	bar := renderProgressBar(done, total, 30)
	fmt.Fprintf(&b, "  %s  %d / %d\n\n", bar, done, total)

	if m.current < total {
		current := m.toClone[m.current]
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), current.Name)
		fmt.Fprintf(&b, "    %s\n", bsDimStyle.Render(current.Project.Path))
	}

	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n%s %d failed (full errors after exit)\n",
			bsErrStyle.Render("✗"), len(m.errors))
	}

	b.WriteString("\n")
	b.WriteString(bsHelpStyle.Render("[ctrl+c] abort"))
	return b.String()
}

func (m bootstrapModel) viewBranchPrompt() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Default branch needed "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  Project: %s\n\n", bsHeaderStyle.Render(m.branchProject))

	if m.branchInputMode {
		b.WriteString("  Enter branch name:\n\n")
		b.WriteString("    " + m.branchInput.View() + "\n\n")
		b.WriteString(bsHelpStyle.Render("[enter] confirm   [esc] back to list"))
		return b.String()
	}

	if len(m.branchCandidates) == 0 {
		b.WriteString(bsDimStyle.Render("  No candidates found.\n\n"))
	} else {
		b.WriteString("  Select default branch:\n\n")
		for i, c := range m.branchCandidates {
			cursor := "  "
			line := c
			if i == m.branchCursor {
				cursor = bsCursorStyle.Render("▸ ")
				line = bsSelectedStyle.Render(c)
			}
			fmt.Fprintf(&b, "    %s%s\n", cursor, line)
		}
		b.WriteString("\n")
	}

	b.WriteString(bsHelpStyle.Render("[↑↓] move   [enter] pick   [i] type custom   [esc] skip project"))
	return b.String()
}

func (m bootstrapModel) viewDone() string {
	var b strings.Builder
	b.WriteString(bsTitleStyle.Render(" Bootstrap finished "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d cloned\n", bsCheckStyle.Render("✓"), len(m.successes))
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

// renderProgressBar draws a simple [█████░░░░░] bar.
func renderProgressBar(done, total, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return bsBarFilledStyle.Render(strings.Repeat("█", filled)) +
		bsBarEmptyStyle.Render(strings.Repeat("░", width-filled))
}

// indent prefixes every line of s with prefix. Used for nesting git stderr
// inside the post-exit error report.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Styles
// =============================================================================

var (
	bsTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	bsHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	bsDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	bsHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	bsCursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	bsSelectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))

	bsCheckStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	bsWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3"))

	bsErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))

	bsArrowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))

	bsBarFilledStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6"))

	bsBarEmptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	errorBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Bold(true)
)
