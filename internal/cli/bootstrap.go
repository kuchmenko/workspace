package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/tui"
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

	if dryRun {
		printPlanText(plan)
		return nil
	}

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
	p := tui.NewProgram(model, tui.WithAltScreen())
	program = p
	defer func() { program = nil }()
	finalRaw, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("TUI crashed: %w", runErr)
	}
	final := finalRaw.(bootstrapModel)

	if final.canceled {
		fmt.Println("Bootstrap canceled by user.")
		return nil
	}

	if len(final.errors) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, errorBannerStyle.Render("Bootstrap finished with errors:"))
		for _, e := range final.errors {
			fmt.Fprintf(os.Stderr, "\n  %s\n", e.project)
			fmt.Fprintln(os.Stderr, indent(strings.TrimSpace(e.err.Error()), "    "))
		}
	}

	if final.sidecar != nil && len(final.sidecar.Done) > 0 {
		if err := commitBootstrap(final.sidecar); err != nil {
			return fmt.Errorf("commit bootstrap: %w", err)
		}

		if err := bootstrap.Delete(wsRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove sidecar: %v\n", err)
		}
	}

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
	current   int
	successes []string
	errors    []bootstrapError
	canceled  bool

	spinner tui.Spinner
	sidecar *bootstrap.Sidecar

	branchPrompt branchprompt.Model
	branchAnswer chan branchAnswer
}

type branchAnswer struct {
	branch string
	err    error
}

type cloneDoneMsg struct {
	index   int
	project string
	res     *git.CloneResult
	err     error
}
type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

type allDoneMsg struct{}

var program *tui.Program

func newBootstrapModel(plan *bootstrap.Plan, toClone []bootstrap.PlanItem, resume map[string]bootstrap.DoneEntry) bootstrapModel {
	sp := tui.NewSpinner()
	sp.SetStyle(tui.DotSpinner)
	sp.SetTextStyle(tui.NewStyle().Foreground("6"))

	sc := bootstrap.New(wsRoot)
	for k, v := range resume {
		_ = sc.Set(k, v)
	}

	return bootstrapModel{
		step:    bsStepPlan,
		plan:    plan,
		toClone: toClone,
		spinner: sp,
		sidecar: sc,
	}
}

func (m bootstrapModel) Init() tui.Cmd {
	return m.spinner.Tick
}

func (m bootstrapModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

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
	case bsStepPlan:
		return m.updatePlan(msg)
	case bsStepCloning:
		return m.updateCloning(msg)
	case bsStepBranchPrompt:
		return m.updateBranchPrompt(msg)
	case bsStepDone:
		if _, ok := msg.(tui.KeyMsg); ok {
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m bootstrapModel) updatePlan(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			if len(m.toClone) == 0 {
				m.step = bsStepDone
				return m, tui.Quit
			}

			if err := bootstrap.Save(m.sidecar); err != nil {
				m.errors = append(m.errors, bootstrapError{project: "<sidecar>", err: err})
				return m, tui.Quit
			}
			conflict.Notify("ws: bootstrap started",
				fmt.Sprintf("%s: cloning %d projects", wsRoot, len(m.toClone)))
			m.step = bsStepCloning
			m.stepChangedAt = time.Now()
			return m, tui.Batch(m.spinner.Tick, m.startClone(0))
		case "n", "N", "escape":
			m.canceled = true
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m bootstrapModel) startClone(index int) tui.Cmd {
	if index >= len(m.toClone) {
		return func() tui.Msg { return allDoneMsg{} }
	}
	item := m.toClone[index]
	return func() tui.Msg {
		proj := item.Project

		ch := make(chan branchAnswer, 1)
		opts := git.CloneOptions{
			Logf: func(format string, args ...interface{}) {
			},
			PromptDefaultBranch: func(name string, candidates []string) (string, error) {
				program.Send(needsBranchMsg{
					project:    name,
					candidates: candidates,
					answer:     ch,
				})
				ans := <-ch
				return ans.branch, ans.err
			},
		}
		res, err := git.CloneIntoLayout(wsRoot, item.Name, &proj, opts)

		return cloneDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

func (m bootstrapModel) updateCloning(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case needsBranchMsg:

		m.step = bsStepBranchPrompt
		m.stepChangedAt = time.Now()
		m.branchPrompt = branchprompt.NewModel(msg.project, msg.candidates)
		m.branchAnswer = msg.answer
		return m, m.branchPrompt.Init()

	case cloneDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, bootstrapError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)

			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = bootstrap.Save(m.sidecar)
			}
		}
		m.current = msg.index + 1

		if m.current > 0 && m.current%5 == 0 && m.current < len(m.toClone) {
			conflict.Notify("ws: bootstrap progress",
				fmt.Sprintf("%d/%d cloned", m.current, len(m.toClone)))
		}
		if m.current >= len(m.toClone) {
			m.step = bsStepDone
			return m, tui.Quit
		}
		return m, m.startClone(m.current)

	case allDoneMsg:
		m.step = bsStepDone
		return m, tui.Quit
	}
	return m, nil
}

func (m bootstrapModel) updateBranchPrompt(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case branchprompt.PickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	case branchprompt.CancelledMsg:

		m.resolveBranch("", errors.New("user canceled branch selection"))
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	}

	var cmd tui.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

func (m *bootstrapModel) resolveBranch(branch string, err error) {
	if m.branchAnswer == nil {
		return
	}
	m.branchAnswer <- branchAnswer{branch: branch, err: err}
	m.branchAnswer = nil
}

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
	return m.branchPrompt.View()
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

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

var (
	bsTitleStyle = tui.NewStyle().
			Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	bsHeaderStyle = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	bsDimStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	bsHelpStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	bsCheckStyle = tui.NewStyle().
			Foreground(tui.Color("2"))

	bsWarnStyle = tui.NewStyle().
			Foreground(tui.Color("3"))

	bsErrStyle = tui.NewStyle().
			Foreground(tui.Color("1"))

	bsArrowStyle = tui.NewStyle().
			Foreground(tui.Color("6"))

	bsBarFilledStyle = tui.NewStyle().
				Foreground(tui.Color("6"))

	bsBarEmptyStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	errorBannerStyle = tui.NewStyle().
				Foreground(tui.Color("1")).
				Bold(true)
)
